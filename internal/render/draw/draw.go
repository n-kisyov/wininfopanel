// Package draw turns a profile's display items into rendered frames.
//
// It is the port of InfoPanel's PanelDraw: walk the item tree, dispatch on
// each item's type, and paint it onto a graphics surface. Everything it needs
// arrives through a Frame -- live sensor values, the clock, chart history --
// so the same code renders a live overlay, a USB panel, a web preview, and a
// golden-image test.
package draw

import (
	"image/color"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// Frame carries everything one render pass depends on.
//
// Grouping it here rather than reading globals is what makes rendering
// reproducible: a test supplies fixed sensors and a fixed clock and gets a
// deterministic frame.
type Frame struct {
	// Profile supplies the canvas size, default typography, and font scale.
	Profile *model.Profile
	// Sensors resolves display item bindings. A nil resolver renders every
	// bound item as unavailable, which is correct before any source starts.
	Sensors sensor.Resolver
	// Now drives clocks, calendars, marquee offsets, and gauge easing.
	// The zero value means time.Now.
	Now time.Time

	// History supplies the sample series graphs plot. It may be nil, in which
	// case graphs render their frame and background but no trace.
	History *HistoryStore
	// Images resolves image display items to decoded bitmaps. It may be nil.
	Images ImageSource
	// Smoothing eases bar, donut, and gauge values toward their targets across
	// frames. It may be nil, in which case values change instantly.
	Smoothing *Smoother

	// Design enables editor affordances: grid lines and selection outlines.
	Design bool
	// Selected lists the item IDs to outline, used only when Design is set.
	Selected []string
	// GridSpacing is the design grid pitch in pixels; 0 disables the grid.
	GridSpacing float64
	// GridColor and SelectionColor style the editor affordances.
	GridColor      color.NRGBA
	SelectionColor color.NRGBA
}

// now resolves the frame's clock.
func (f *Frame) now() time.Time {
	if f.Now.IsZero() {
		return time.Now()
	}
	return f.Now
}

// evalContext builds the context display items evaluate themselves against.
func (f *Frame) evalContext(g *graphics.Graphics) *model.EvalContext {
	return &model.EvalContext{
		Sensors: f.Sensors,
		Measure: measurer{g: g},
		Profile: f.Profile,
		Now:     f.now(),
	}
}

// profileID returns the rendering profile's ID, or empty when no profile is
// attached.
func (f *Frame) profileID() string {
	if f.Profile == nil {
		return ""
	}
	return f.Profile.ID
}

// smooth eases a value toward its target for one item, or passes it through
// when no smoother is attached.
func (f *Frame) smooth(id string, target float64) float64 {
	if f.Smoothing == nil {
		return target
	}
	return f.Smoothing.Step(id, target)
}

// isSelected reports whether an item should be outlined.
func (f *Frame) isSelected(id string) bool {
	for _, selected := range f.Selected {
		if selected == id {
			return true
		}
	}
	return false
}

// measurer adapts the graphics surface to the model's TextMeasurer, which is
// how display items compute their bounds without importing the renderer.
type measurer struct{ g *graphics.Graphics }

func (m measurer) Measure(spec model.TextSpec) model.Size {
	size := m.g.MeasureText(graphics.TextSpec{
		Text:      spec.Text,
		Family:    spec.Font,
		Size:      spec.FontSize,
		Bold:      spec.Bold,
		Italic:    spec.Italic,
		Underline: spec.Underline,
		Strikeout: spec.Strikeout,
		Wrap:      spec.Wrap,
		Ellipsis:  spec.Ellipsis,
		Width:     spec.Width,
		Height:    spec.Height,
	})
	return model.Size{Width: size.Width, Height: size.Height}
}

// Render paints a complete frame: background, every visible item in order, and
// any editor affordances.
func Render(g *graphics.Graphics, items model.ItemList, frame Frame) {
	background := color.NRGBA{}
	if frame.Profile != nil {
		background = graphics.ColorOr(frame.Profile.BackgroundColor, color.NRGBA{})
	}
	g.Clear(background)

	if frame.Design && frame.GridSpacing > 0 {
		drawGrid(g, frame)
	}

	ctx := frame.evalContext(g)
	for _, item := range items {
		drawItem(g, item, ctx, &frame)
	}

	if frame.Design {
		drawSelection(g, items, ctx, &frame)
	}
}

// drawItem paints one display item, descending into groups.
//
// Hidden items are skipped here rather than inside each type's routine, so
// visibility behaves identically for every kind.
func drawItem(g *graphics.Graphics, item model.DisplayItem, ctx *model.EvalContext, frame *Frame) {
	if item == nil || item.Base().Hidden {
		return
	}

	switch it := item.(type) {
	case *model.GroupItem:
		for _, child := range it.Items {
			drawItem(g, child, ctx, frame)
		}
	case *model.TextItem:
		drawTextLike(g, &it.ItemBase, &it.TextStyle, it, ctx, frame)
	case *model.ClockItem:
		drawTextLike(g, &it.ItemBase, &it.TextStyle, it, ctx, frame)
	case *model.CalendarItem:
		drawTextLike(g, &it.ItemBase, &it.TextStyle, it, ctx, frame)
	case *model.SensorItem:
		drawTextLike(g, &it.ItemBase, &it.TextStyle, it, ctx, frame)
	case *model.TableItem:
		drawTable(g, it, ctx, frame)
	case *model.GraphItem:
		drawGraph(g, it, ctx, frame)
	case *model.BarItem:
		drawBar(g, it, ctx, frame)
	case *model.DonutItem:
		drawDonut(g, it, ctx, frame)
	case *model.GaugeItem:
		drawGauge(g, it, ctx, frame)
	case *model.ShapeItem:
		drawShape(g, it, frame)
	case *model.SensorImageItem:
		drawImage(g, &it.ImageItem, ctx, frame)
	case *model.HTTPImageItem:
		drawImage(g, &it.ImageItem, ctx, frame)
	case *model.ImageItem:
		drawImage(g, it, ctx, frame)
	}
}

// drawGrid paints the design canvas guide lines beneath the layout.
func drawGrid(g *graphics.Graphics, frame Frame) {
	stroke := graphics.SolidStroke(frame.GridColor, 1)
	if frame.GridColor.A == 0 {
		return
	}

	width, height := float64(g.Width()), float64(g.Height())
	for x := frame.GridSpacing; x < width; x += frame.GridSpacing {
		g.Line(x, 0, x, height, stroke, graphics.Transform{})
	}
	for y := frame.GridSpacing; y < height; y += frame.GridSpacing {
		g.Line(0, y, width, y, stroke, graphics.Transform{})
	}
}

// drawSelection outlines the selected items.
//
// Outlines are painted after the whole layout so a selected item behind
// another still shows its handle, which is what makes overlapping items
// workable on the canvas.
func drawSelection(g *graphics.Graphics, items model.ItemList, ctx *model.EvalContext, frame *Frame) {
	if len(frame.Selected) == 0 {
		return
	}

	stroke := graphics.SolidStroke(frame.SelectionColor, 2)
	for _, item := range model.FlattenAll(items) {
		if !frame.isSelected(item.Base().ID) {
			continue
		}
		bounds := item.Bounds(ctx)
		g.StrokeRect(toRect(bounds), 0, stroke,
			graphics.Transform{Rotation: float64(item.Base().Rotation)})
	}

	// Groups are outlined by their own union bounds, not their children's.
	for _, item := range items {
		group, ok := item.(*model.GroupItem)
		if !ok || !frame.isSelected(group.ID) {
			continue
		}
		g.StrokeRect(toRect(group.Bounds(ctx)), 0, stroke,
			graphics.Transform{Rotation: float64(group.Rotation)})
	}
}

// toRect converts model bounds into surface coordinates.
func toRect(r model.Rect) graphics.Rect {
	return graphics.Rect{X: r.Left, Y: r.Top, W: r.Width(), H: r.Height()}
}
