// Package model defines the profile, settings, and display-item data types
// that make up a wininfopanel layout.
//
// Types here are plain data. Anything needing a rasterizer — text measurement,
// actual drawing — lives in internal/render; the model reaches it only through
// the small TextMeasurer interface passed in an EvalContext.
package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// ItemKind is the discriminator distinguishing display-item types, both in the
// UI and in serialized profiles.
type ItemKind string

const (
	KindText        ItemKind = "Text"
	KindSensor      ItemKind = "Sensor"
	KindTable       ItemKind = "Table"
	KindClock       ItemKind = "Clock"
	KindCalendar    ItemKind = "Calendar"
	KindImage       ItemKind = "Image"
	KindHTTPImage   ItemKind = "HttpImage"
	KindSensorImage ItemKind = "SensorImage"
	KindGraph       ItemKind = "Graph"
	KindBar         ItemKind = "Bar"
	KindDonut       ItemKind = "Donut"
	KindGauge       ItemKind = "Gauge"
	KindShape       ItemKind = "Shape"
	KindGroup       ItemKind = "Group"
)

// DisplayItem is one element of a profile layout.
type DisplayItem interface {
	// Base exposes the position, name, and visibility every item carries.
	Base() *ItemBase
	// Kind reports the item's type discriminator.
	Kind() ItemKind
	// Clone returns a faithful deep copy, identity included.
	//
	// It is a copy of the same item, not a new one: reading a layout hands out
	// clones, and an ID that changed on the way out would make every
	// ID-addressed operation miss. Use Duplicate to make a genuinely new item.
	Clone() DisplayItem
	// Bounds returns the item's extent in profile coordinates, before the
	// item's own rotation is applied.
	Bounds(ctx *EvalContext) Rect
}

// TextEvaluator is implemented by items that render as a string.
type TextEvaluator interface {
	// EvaluateTextAndColor returns the text to draw and the color to draw it
	// in. Both are resolved together because threshold coloring depends on the
	// same sensor read that produces the text.
	EvaluateTextAndColor(ctx *EvalContext) (text string, color string)
}

// SensorBound is implemented by items driven by a sensor value.
type SensorBound interface {
	Binding() *SensorBinding
}

// ItemBase carries the fields shared by every display item.
type ItemBase struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`

	// Rotation is applied in degrees about the item's center.
	Rotation int `json:"rotation,omitempty"`

	// Hidden excludes the item from rendering while keeping it in the layout.
	Hidden bool `json:"hidden,omitempty"`
	// Locked prevents selection and dragging on the design canvas.
	Locked bool `json:"locked,omitempty"`
}

// Base implements DisplayItem.
func (b *ItemBase) Base() *ItemBase { return b }

// newItemBase returns a base with a fresh identity.
func newItemBase(name string) ItemBase {
	return ItemBase{ID: uuid.NewString(), Name: name, X: 100, Y: 100}
}

// reidentify assigns a fresh ID, used when duplicating.
func (b *ItemBase) reidentify() { b.ID = uuid.NewString() }

// TextSpec describes a run of text well enough to measure or draw it.
type TextSpec struct {
	Text      string
	Font      string
	FontStyle string
	FontSize  int
	Bold      bool
	Italic    bool
	Underline bool
	Strikeout bool
	Wrap      bool
	Ellipsis  bool
	// Width bounds the text box; 0 means unconstrained.
	Width int
	// Height bounds the text box; 0 means unconstrained.
	Height int
	// Scale is the profile's font scale factor.
	Scale float64
}

// TextMeasurer reports the rendered extent of a run of text. internal/render
// provides the implementation.
type TextMeasurer interface {
	Measure(TextSpec) Size
}

// nopMeasurer estimates text extent without a rasterizer, so that bounds
// queries still return something usable in tests and headless callers.
type nopMeasurer struct{}

func (nopMeasurer) Measure(s TextSpec) Size {
	// A rough monospace approximation: adequate for ordering and hit-testing
	// fallbacks, never for layout that the user will see.
	const widthPerPoint = 0.6
	scale := s.Scale
	if scale <= 0 {
		scale = 1
	}
	h := float64(s.FontSize) * scale
	w := float64(len([]rune(s.Text))) * h * widthPerPoint
	if s.Width > 0 && w > float64(s.Width) {
		w = float64(s.Width)
	}
	return Size{Width: w, Height: h}
}

// EvalContext supplies everything an item needs to evaluate itself: live
// sensor values, a way to measure text, and the profile it belongs to.
//
// The zero value is usable — sensors resolve to nothing and text is estimated —
// which keeps bounds queries safe to call from anywhere.
type EvalContext struct {
	Sensors sensor.Resolver
	Measure TextMeasurer
	Profile *Profile
	Now     time.Time
}

func (c *EvalContext) sensors() sensor.Resolver {
	if c == nil || c.Sensors == nil {
		return sensor.NopResolver{}
	}
	return c.Sensors
}

func (c *EvalContext) measurer() TextMeasurer {
	if c == nil || c.Measure == nil {
		return nopMeasurer{}
	}
	return c.Measure
}

func (c *EvalContext) now() time.Time {
	if c == nil || c.Now.IsZero() {
		return time.Now()
	}
	return c.Now
}

// fontScale returns the profile's font scale, defaulting to 1.
func (c *EvalContext) fontScale() float64 {
	if c == nil || c.Profile == nil || c.Profile.FontScale <= 0 {
		return 1
	}
	return float64(c.Profile.FontScale)
}

// Flatten expands an item into the leaf items that actually draw.
//
// Groups contribute their children recursively; a gauge contributes its image
// frames, since those are what the renderer ultimately blits.
func Flatten(item DisplayItem) []DisplayItem {
	switch it := item.(type) {
	case *GroupItem:
		var out []DisplayItem
		for _, child := range it.Items {
			out = append(out, Flatten(child)...)
		}
		return out
	case *GaugeItem:
		out := make([]DisplayItem, 0, len(it.Images))
		for i := range it.Images {
			out = append(out, it.Images[i])
		}
		return out
	default:
		return []DisplayItem{item}
	}
}

// FlattenAll expands every item in a layout.
func FlattenAll(items []DisplayItem) []DisplayItem {
	var out []DisplayItem
	for _, item := range items {
		out = append(out, Flatten(item)...)
	}
	return out
}

// FindByID locates an item anywhere in the tree, descending into groups.
func FindByID(items []DisplayItem, id string) (DisplayItem, bool) {
	for _, item := range items {
		if item.Base().ID == id {
			return item, true
		}
		if group, ok := item.(*GroupItem); ok {
			if found, ok := FindByID(group.Items, id); ok {
				return found, true
			}
		}
	}
	return nil, false
}

// CloneAll deep-copies a layout, preserving every item's identity.
func CloneAll(items []DisplayItem) []DisplayItem {
	if items == nil {
		return nil
	}
	out := make([]DisplayItem, len(items))
	for i, item := range items {
		out[i] = item.Clone()
	}
	return out
}

// Duplicate returns a copy of an item as a genuinely new one, with a fresh ID
// throughout its tree.
//
// This is what "duplicate this item" means, as distinct from Clone, which
// hands out a faithful copy of the same item.
func Duplicate(item DisplayItem) DisplayItem {
	copied := item.Clone()
	reidentifyTree(copied)
	return copied
}

// DuplicateAll duplicates a whole layout.
func DuplicateAll(items []DisplayItem) []DisplayItem {
	if items == nil {
		return nil
	}
	out := make([]DisplayItem, len(items))
	for i, item := range items {
		out[i] = Duplicate(item)
	}
	return out
}

// reidentifyTree assigns fresh IDs to an item and everything beneath it.
func reidentifyTree(item DisplayItem) {
	item.Base().reidentify()

	switch it := item.(type) {
	case *GroupItem:
		for _, child := range it.Items {
			reidentifyTree(child)
		}
	case *GaugeItem:
		for _, frame := range it.Images {
			frame.reidentify()
		}
	}
}
