// Package media loads and caches the bitmaps display items draw.
//
// It sits behind draw.ImageSource so the drawing code deals only in decoded
// images, never in file paths, HTTP responses, or GIF frame timing.
package media

import (
	"fmt"
	"image"
	"image/gif"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	// Registering the decoders is the whole point of these imports.
	_ "image/jpeg"
	_ "image/png"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/logging"
)

// Loader resolves image display items to bitmaps, caching decoded results.
//
// It is safe for concurrent use: the render loop and the design preview both
// resolve images.
type Loader struct {
	log *slog.Logger
	// assetRoot resolves an item's relative path, which is what makes a
	// profile portable between machines.
	assetRoot func(profileID string) (string, error)
	// now is injectable so GIF timing is testable.
	now func() time.Time

	mu     sync.RWMutex
	cache  map[string]*entry
	failed map[string]time.Time
}

// entry is one decoded image, still or animated.
type entry struct {
	// still holds a single-frame image.
	still image.Image
	// frames and delays hold an animation; delays are per frame.
	frames []image.Image
	delays []time.Duration
	// total is the animation's full cycle length.
	total time.Duration

	loadedAt time.Time
	modTime  time.Time
	size     int64
}

// isAnimated reports whether the entry holds more than one frame.
func (e *entry) isAnimated() bool { return len(e.frames) > 1 }

// frameAt picks the animation frame for a moment, looping.
func (e *entry) frameAt(elapsed time.Duration) image.Image {
	if !e.isAnimated() {
		return e.still
	}
	if e.total <= 0 {
		return e.frames[0]
	}

	position := elapsed % e.total
	for i, delay := range e.delays {
		if position < delay {
			return e.frames[i]
		}
		position -= delay
	}
	return e.frames[len(e.frames)-1]
}

// failureCooldown is how long a failed load is remembered before being retried.
// Without it, a missing file would be reopened on every frame.
const failureCooldown = 10 * time.Second

// Options configures a Loader.
type Options struct {
	// AssetRoot resolves a profile's asset directory, used for items whose
	// path is relative.
	AssetRoot func(profileID string) (string, error)
}

// NewLoader returns an empty loader.
func NewLoader(opts Options) *Loader {
	return &Loader{
		log:       logging.For("render.media"),
		assetRoot: opts.AssetRoot,
		now:       time.Now,
		cache:     make(map[string]*entry),
		failed:    make(map[string]time.Time),
	}
}

// Resolve implements draw.ImageSource.
//
// Only file-backed images are handled here; URL and RTSP sources are resolved
// by the fetchers layered on top of this loader.
func (l *Loader) Resolve(profileID string, item *model.ImageItem) (image.Image, bool) {
	if item == nil || item.Source != model.ImageFile {
		return nil, false
	}

	path, err := l.resolvePath(profileID, item)
	if err != nil {
		return nil, false
	}

	e, ok := l.load(path)
	if !ok {
		return nil, false
	}

	if !e.isAnimated() {
		return e.still, e.still != nil
	}
	return e.frameAt(l.now().Sub(e.loadedAt)), true
}

// resolvePath turns an item's stored path into an absolute one.
//
// Relative paths are resolved against the owning profile's asset directory,
// which is what lets a profile and its artwork move between machines together.
func (l *Loader) resolvePath(profileID string, item *model.ImageItem) (string, error) {
	if item.Path == "" {
		return "", fmt.Errorf("image item has no path")
	}
	if !item.Relative {
		return item.Path, nil
	}
	if l.assetRoot == nil {
		return "", fmt.Errorf("relative image path %q needs an asset root", item.Path)
	}
	if profileID == "" {
		return "", fmt.Errorf("relative image path %q needs a profile to resolve against", item.Path)
	}

	root, err := l.assetRoot(profileID)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, item.Path), nil
}

// load returns a cached entry, decoding the file if needed.
func (l *Loader) load(path string) (*entry, bool) {
	info, statErr := os.Stat(path)

	l.mu.RLock()
	cached, hit := l.cache[path]
	failedAt, recentlyFailed := l.failed[path]
	l.mu.RUnlock()

	// A file edited on disk should be picked up without restarting the app.
	if hit && statErr == nil &&
		cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached, true
	}

	if recentlyFailed && l.now().Sub(failedAt) < failureCooldown {
		return nil, false
	}

	if statErr != nil {
		l.recordFailure(path, statErr)
		return nil, false
	}

	decoded, err := decodeFile(path)
	if err != nil {
		l.recordFailure(path, err)
		return nil, false
	}

	decoded.loadedAt = l.now()
	decoded.modTime = info.ModTime()
	decoded.size = info.Size()

	l.mu.Lock()
	l.cache[path] = decoded
	delete(l.failed, path)
	l.mu.Unlock()

	return decoded, true
}

func (l *Loader) recordFailure(path string, err error) {
	l.mu.Lock()
	_, alreadyFailed := l.failed[path]
	l.failed[path] = l.now()
	l.mu.Unlock()

	// Log the first failure only: a broken path would otherwise fill the log
	// at the frame rate.
	if !alreadyFailed {
		l.log.Warn("could not load image", "path", path, "error", err)
	}
}

// Forget drops a cached image, so the next resolve re-reads it.
func (l *Loader) Forget(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.cache, path)
	delete(l.failed, path)
}

// Clear empties the cache.
func (l *Loader) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cache = make(map[string]*entry)
	l.failed = make(map[string]time.Time)
}

// decodeFile reads an image from disk, expanding animations into frames.
func decodeFile(path string) (*entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if filepath.Ext(path) == ".gif" || filepath.Ext(path) == ".GIF" {
		animation, err := gif.DecodeAll(f)
		if err == nil {
			return entryFromGIF(animation), nil
		}
		// A single-frame GIF that DecodeAll rejects can still be a valid
		// still image, so fall through rather than failing outright.
		if _, seekErr := f.Seek(0, 0); seekErr != nil {
			return nil, err
		}
	}

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return &entry{still: img}, nil
}

// entryFromGIF composites a GIF's frames.
//
// GIF frames are deltas against what came before, so each is painted onto a
// running canvas; drawing the raw frames would show only the changed regions.
func entryFromGIF(animation *gif.GIF) *entry {
	if len(animation.Image) == 0 {
		return &entry{}
	}

	bounds := animation.Image[0].Bounds()
	for _, frame := range animation.Image[1:] {
		bounds = bounds.Union(frame.Bounds())
	}

	e := &entry{
		frames: make([]image.Image, 0, len(animation.Image)),
		delays: make([]time.Duration, 0, len(animation.Image)),
	}

	canvas := image.NewRGBA(bounds)
	for i, frame := range animation.Image {
		drawOver(canvas, frame)

		// Each composited frame is snapshotted, since the canvas keeps
		// changing underneath.
		snapshot := image.NewRGBA(bounds)
		copy(snapshot.Pix, canvas.Pix)
		e.frames = append(e.frames, snapshot)

		// GIF delays are in hundredths of a second. Zero means "as fast as
		// possible", which browsers and viewers universally treat as 100ms.
		delay := time.Duration(animation.Delay[i]) * 10 * time.Millisecond
		if delay <= 0 {
			delay = 100 * time.Millisecond
		}
		e.delays = append(e.delays, delay)
		e.total += delay
	}

	if len(e.frames) == 1 {
		e.still = e.frames[0]
	}
	return e
}

// drawOver composites one GIF frame onto the running canvas.
func drawOver(canvas *image.RGBA, frame *image.Paletted) {
	bounds := frame.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := frame.At(x, y)
			if _, _, _, a := c.RGBA(); a == 0 {
				continue // transparent pixels leave the previous frame showing
			}
			canvas.Set(x, y, c)
		}
	}
}
