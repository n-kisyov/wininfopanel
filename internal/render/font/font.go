// Package font discovers the fonts installed on Windows and caches parsed
// faces for the renderer.
//
// Profiles store a font by family name, so a layout authored on one machine
// names a font that may not exist on another. Resolution therefore degrades
// rather than fails: an exact match, else a close relative, else the system
// default. A missing font must never stop a panel from drawing.
package font

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// Style selects a weight and slant within a family.
type Style struct {
	Bold   bool
	Italic bool
}

// String renders the style the way font filenames spell it.
func (s Style) String() string {
	switch {
	case s.Bold && s.Italic:
		return "Bold Italic"
	case s.Bold:
		return "Bold"
	case s.Italic:
		return "Italic"
	default:
		return "Regular"
	}
}

// Family is an installed font family and the files backing its styles.
type Family struct {
	Name string
	// files maps a style to the font file providing it.
	files map[Style]string
}

// Styles lists the styles this family has files for.
func (f *Family) Styles() []Style {
	out := make([]Style, 0, len(f.files))
	for style := range f.files {
		out = append(out, style)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// faceKey identifies a rasterized face in the cache.
type faceKey struct {
	family string
	style  Style
	size   float64
}

// Cache resolves family names to rendered faces, parsing each font file once.
//
// It is safe for concurrent use: the render loop and the design canvas both
// measure text.
type Cache struct {
	mu sync.RWMutex

	// families is the installed font index, built on first use.
	families map[string]*Family
	// order preserves a sorted family list for the UI's font picker.
	order []string
	// parsed holds font files decoded into a sfnt.Font, keyed by path.
	parsed map[string]*sfnt.Font
	// faces holds sized faces, the actual rasterization input.
	faces map[faceKey]font.Face
	// indexedPaths records font files already claimed by a registry entry, so
	// the directory sweep does not re-add them under a filename-derived name.
	indexedPaths map[string]bool

	// fallback is the family used when a requested one is unavailable.
	fallback string
	loaded   bool
	loadErr  error
}

// NewCache returns an empty cache. The font index is built lazily on first
// use, which keeps startup off the font directory.
func NewCache() *Cache {
	return &Cache{
		families:     make(map[string]*Family),
		parsed:       make(map[string]*sfnt.Font),
		faces:        make(map[faceKey]font.Face),
		indexedPaths: make(map[string]bool),
		fallback:     DefaultFamily,
	}
}

// DefaultFamily is the fallback family. Segoe UI ships with every supported
// Windows version.
const DefaultFamily = "Segoe UI"

// Families lists the installed families, sorted.
func (c *Cache) Families() ([]string, error) {
	if err := c.ensureLoaded(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out, nil
}

// Has reports whether a family is installed.
func (c *Cache) Has(family string) bool {
	if err := c.ensureLoaded(); err != nil {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.families[normalizeFamily(family)]
	return ok
}

// Face returns a rasterizable face for the requested family, style, and size
// in pixels.
//
// The returned face is shared and must not be closed by the caller.
func (c *Cache) Face(family string, style Style, size float64) (font.Face, error) {
	if size <= 0 {
		return nil, fmt.Errorf("font size must be positive, got %v", size)
	}
	if err := c.ensureLoaded(); err != nil {
		return nil, err
	}

	resolvedKey, resolvedStyle := c.resolve(family, style)
	key := faceKey{family: resolvedKey, style: resolvedStyle, size: size}

	c.mu.RLock()
	if face, ok := c.faces[key]; ok {
		c.mu.RUnlock()
		return face, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// Another goroutine may have built it while the write lock was contended.
	if face, ok := c.faces[key]; ok {
		return face, nil
	}

	fam, ok := c.families[resolvedKey]
	if !ok {
		return nil, fmt.Errorf("font family %q is not installed and no fallback was available", family)
	}
	path, ok := fam.files[resolvedStyle]
	if !ok {
		return nil, fmt.Errorf("font family %q has no %s style", fam.Name, resolvedStyle)
	}

	parsed, err := c.parseLocked(path)
	if err != nil {
		return nil, err
	}

	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size: size,
		DPI:  72, // Size is then in pixels, which is what the renderer works in.
		// Full hinting keeps small text legible on low-resolution USB panels,
		// where a 12px readout is common.
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("build face for %s %s at %vpx: %w", fam.Name, resolvedStyle, size, err)
	}

	c.faces[key] = face
	return face, nil
}

// parseLocked decodes a font file, caching the result. Caller holds c.mu.
func (c *Cache) parseLocked(path string) (*sfnt.Font, error) {
	if parsed, ok := c.parsed[path]; ok {
		return parsed, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read font file %s: %w", path, err)
	}

	parsed, err := sfnt.Parse(data)
	if err != nil {
		// TrueType collections (.ttc) hold several fonts; sfnt.Parse handles
		// only single fonts, so fall back to the collection reader.
		collection, cErr := sfnt.ParseCollection(data)
		if cErr != nil {
			return nil, fmt.Errorf("parse font file %s: %w", path, err)
		}
		parsed, err = collection.Font(0)
		if err != nil {
			return nil, fmt.Errorf("read first font from collection %s: %w", path, err)
		}
	}

	c.parsed[path] = parsed
	return parsed, nil
}

// resolve maps a requested family and style onto ones that actually exist,
// returning the normalized key into c.families rather than the display name.
//
// The search widens in steps: the exact family, then a family whose name
// contains the request (so "Segoe UI Variable" satisfies "Segoe UI"), then the
// configured fallback. Style degrades the same way, since a family may ship
// Regular but not Bold Italic.
func (c *Cache) resolve(family string, style Style) (string, Style) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	name := normalizeFamily(family)
	fam, ok := c.families[name]
	if !ok {
		fam, ok = c.findRelatedLocked(name)
	}
	if !ok {
		fam, ok = c.families[normalizeFamily(c.fallback)]
	}
	if !ok {
		// Nothing matched; return the request unchanged so the caller reports
		// a useful error naming what was actually asked for.
		return name, style
	}

	return normalizeFamily(fam.Name), closestStyle(fam, style)
}

// findRelatedLocked looks for a family whose name contains the request, or is
// contained by it. Caller holds c.mu.
func (c *Cache) findRelatedLocked(name string) (*Family, bool) {
	var best *Family
	for candidate, fam := range c.families {
		if !strings.Contains(candidate, name) && !strings.Contains(name, candidate) {
			continue
		}
		// Prefer the shortest match: "Segoe UI" over "Segoe UI Symbol".
		if best == nil || len(fam.Name) < len(best.Name) {
			best = fam
		}
	}
	return best, best != nil
}

// closestStyle picks the nearest available style, dropping italic before bold
// since a missing slant is less noticeable than a missing weight.
func closestStyle(fam *Family, want Style) Style {
	if _, ok := fam.files[want]; ok {
		return want
	}

	candidates := []Style{
		{Bold: want.Bold},
		{Italic: want.Italic},
		{},
	}
	for _, candidate := range candidates {
		if _, ok := fam.files[candidate]; ok {
			return candidate
		}
	}

	// Nothing conventional matched; take whatever the family does have.
	for style := range fam.files {
		return style
	}
	return want
}

// normalizeFamily makes family names comparable across the spelling variations
// profiles contain.
func normalizeFamily(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ensureLoaded builds the font index once.
func (c *Cache) ensureLoaded() error {
	c.mu.RLock()
	loaded, err := c.loaded, c.loadErr
	c.mu.RUnlock()
	if loaded {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.loadErr
	}

	c.loadErr = c.scanLocked()
	c.loaded = true
	return c.loadErr
}

// AddFontFile registers a font file explicitly, for fonts shipped with a
// profile rather than installed system-wide.
func (c *Cache) AddFontFile(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	family, style, ok := familyFromFilename(path)
	if !ok {
		return fmt.Errorf("cannot infer a family name from %s", filepath.Base(path))
	}
	c.addLocked(family, style, path)
	c.sortLocked()
	return nil
}

// addLocked indexes one font file. Caller holds c.mu.
func (c *Cache) addLocked(family string, style Style, path string) {
	key := normalizeFamily(family)
	fam, ok := c.families[key]
	if !ok {
		fam = &Family{Name: family, files: make(map[Style]string)}
		c.families[key] = fam
	}
	// First file wins: the registry is indexed before the directory sweep, and
	// duplicates are usually the same face under a different name.
	if _, exists := fam.files[style]; !exists {
		fam.files[style] = path
	}
	c.indexedPaths[strings.ToLower(path)] = true
}

func (c *Cache) sortLocked() {
	c.order = c.order[:0]
	for _, fam := range c.families {
		c.order = append(c.order, fam.Name)
	}
	sort.Strings(c.order)
}
