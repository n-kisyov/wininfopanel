// Package display drives desktop overlay windows: one per active profile,
// each rendered on its own loop and composited onto the desktop with
// per-pixel alpha.
package display

import (
	"context"
	"image"
	"log/slog"
	"sync"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/render/draw"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/internal/winapi"
)

// LayoutProvider supplies the current layout for a profile.
//
// The overlay re-reads through this on every frame rather than holding a
// snapshot, so edits on the design canvas appear immediately without the
// manager having to push updates.
type LayoutProvider interface {
	// Profile returns the profile's current settings.
	Profile(profileID string) (*model.Profile, bool)
	// WithLayout lends the profile's display items under a read lock.
	WithLayout(profileID string, fn func(model.ItemList)) error
}

// Options configures an Overlay.
type Options struct {
	ProfileID string

	Layouts LayoutProvider
	Sensors sensor.Resolver
	Fonts   *font.Cache
	Images  draw.ImageSource
	History *draw.HistoryStore

	// FrameRate is the target repaint rate. Values below one fall back to the
	// default.
	FrameRate int
	// GraphUpdateRate is the interval in milliseconds between chart samples.
	GraphUpdateRate int

	// OnPositionChanged persists a drag.
	OnPositionChanged func(profileID string, x, y int)
	// OnRightClick raises the overlay's context menu.
	OnRightClick func(profileID string)
}

const (
	defaultFrameRate       = 15
	defaultGraphUpdateRate = 1000
)

// Overlay is one profile rendered into a desktop window.
type Overlay struct {
	log  *slog.Logger
	opts Options

	window *winapi.LayeredWindow

	mu sync.Mutex
	// smoothing is per overlay so two windows showing the same profile animate
	// independently.
	smoothing *draw.Smoother
	// surface is reused between frames; it is rebuilt when the profile resizes.
	surface *graphics.Graphics
	width   int
	height  int
	// fontScale is tracked so the surface is rebuilt when it changes.
	fontScale float64

	stopped chan struct{}
}

// New prepares an overlay for a profile. Nothing appears until Run is called.
func New(opts Options) *Overlay {
	if opts.FrameRate < 1 {
		opts.FrameRate = defaultFrameRate
	}
	if opts.GraphUpdateRate < 1 {
		opts.GraphUpdateRate = defaultGraphUpdateRate
	}
	if opts.Fonts == nil {
		opts.Fonts = font.NewCache()
	}

	return &Overlay{
		log:       logging.For("display.overlay").With("profile", opts.ProfileID),
		opts:      opts,
		smoothing: draw.NewSmoother(draw.CyclesForFrameRate(opts.FrameRate)),
		stopped:   make(chan struct{}),
	}
}

// Run creates the window and renders into it until ctx is cancelled.
func (o *Overlay) Run(ctx context.Context) error {
	defer close(o.stopped)

	profile, ok := o.opts.Layouts.Profile(o.opts.ProfileID)
	if !ok {
		return errProfileMissing{id: o.opts.ProfileID}
	}

	// A position saved against a monitor that is no longer attached would put
	// the panel somewhere invisible, which looks like a failure to start.
	x, y := winapi.ClampToScreens(profile.WindowX, profile.WindowY, profile.Width, profile.Height)

	o.window = winapi.NewLayeredWindow(winapi.LayeredWindowOptions{
		Title:     "wininfopanel - " + profile.Name,
		X:         x,
		Y:         y,
		Width:     profile.Width,
		Height:    profile.Height,
		Topmost:   profile.Topmost,
		Draggable: profile.Drag,
		OnMoved: func(nx, ny int) {
			if o.opts.OnPositionChanged != nil {
				o.opts.OnPositionChanged(o.opts.ProfileID, nx, ny)
			}
		},
		OnRightClick: func() {
			if o.opts.OnRightClick != nil {
				o.opts.OnRightClick(o.opts.ProfileID)
			}
		},
	})

	// The window owns an OS thread for its whole life, so it runs on its own
	// goroutine and the render loop drives it by posting frames.
	windowErr := make(chan error, 1)
	go func() { windowErr <- o.window.Run() }()

	select {
	case <-o.window.Ready():
	case err := <-windowErr:
		return err
	case <-ctx.Done():
		o.window.Close()
		return ctx.Err()
	}

	o.log.Info("overlay started", "size", profile.Width, "x", x, "y", y)

	renderErr := o.renderLoop(ctx)

	o.window.Close()
	<-o.window.Done()

	if renderErr != nil {
		return renderErr
	}
	return <-windowErr
}

// Stopped is closed once the overlay has fully shut down.
func (o *Overlay) Stopped() <-chan struct{} { return o.stopped }

// renderLoop paints frames at the target rate until ctx is cancelled.
func (o *Overlay) renderLoop(ctx context.Context) error {
	interval := time.Second / time.Duration(o.opts.FrameRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sampleInterval := time.Duration(o.opts.GraphUpdateRate) * time.Millisecond
	lastSample := time.Time{}

	for {
		now := time.Now()

		// Chart history is sampled on its own cadence, which is usually far
		// slower than the frame rate: a graph's axis is time, not frames.
		if o.opts.History != nil && now.Sub(lastSample) >= sampleInterval {
			o.sampleHistory(now)
			lastSample = now
		}

		if frame := o.renderFrame(now); frame != nil {
			o.window.Present(frame)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// renderFrame draws one frame and returns it, or nil if the profile has gone.
func (o *Overlay) renderFrame(now time.Time) *image.RGBA {
	profile, ok := o.opts.Layouts.Profile(o.opts.ProfileID)
	if !ok {
		return nil
	}

	g := o.surfaceFor(profile)

	frame := draw.Frame{
		Profile:   profile,
		Sensors:   o.opts.Sensors,
		Now:       now,
		History:   o.opts.History,
		Images:    o.opts.Images,
		Smoothing: o.smoothing,
	}

	err := o.opts.Layouts.WithLayout(o.opts.ProfileID, func(items model.ItemList) {
		draw.Render(g, items, frame)
	})
	if err != nil {
		o.log.Warn("could not read layout", "error", err)
		return nil
	}

	// The surface is reused between frames while the compositor may still be
	// reading the previous one, so hand over a copy.
	return g.Snapshot()
}

// surfaceFor returns the drawing surface, rebuilding it when the profile's
// size or font scale changes.
func (o *Overlay) surfaceFor(profile *model.Profile) *graphics.Graphics {
	o.mu.Lock()
	defer o.mu.Unlock()

	scale := profile.FontScale
	if scale <= 0 {
		scale = 1
	}

	if o.surface != nil && o.width == profile.Width && o.height == profile.Height &&
		o.fontScale == scale {
		return o.surface
	}

	o.surface = graphics.New(profile.Width, profile.Height, graphics.Options{
		Fonts:     o.opts.Fonts,
		FontScale: scale,
	})
	o.width, o.height, o.fontScale = profile.Width, profile.Height, scale
	return o.surface
}

// sampleHistory records the current value of every graphed sensor.
func (o *Overlay) sampleHistory(now time.Time) {
	ctx := &model.EvalContext{Sensors: o.opts.Sensors, Now: now}

	_ = o.opts.Layouts.WithLayout(o.opts.ProfileID, func(items model.ItemList) {
		for _, item := range model.FlattenAll(items) {
			graph, ok := item.(*model.GraphItem)
			if !ok {
				continue
			}
			if value, ok := graph.Value(ctx); ok {
				o.opts.History.Sample(graph.Key, value, now)
			}
		}
	})
}

// SetTopmost raises or lowers the overlay.
func (o *Overlay) SetTopmost(topmost bool) {
	if o.window != nil {
		o.window.SetTopmost(topmost)
	}
}

// Move repositions the overlay.
func (o *Overlay) Move(x, y int) {
	if o.window != nil {
		o.window.Move(x, y)
	}
}

// errProfileMissing reports a profile that vanished before its overlay
// started.
type errProfileMissing struct{ id string }

func (e errProfileMissing) Error() string {
	return "profile " + e.id + " not found"
}
