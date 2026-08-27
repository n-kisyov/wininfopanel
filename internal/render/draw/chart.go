package draw

import (
	"image/color"
	"math"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
)

// The three chart types frame themselves differently, following InfoPanel:
// a graph gets a background rectangle and a border, a bar draws its own
// rounded track and border, and a donut has neither -- its "background" is the
// unfilled part of the ring.

// chartArea returns the rectangle a chart occupies.
func chartArea(base *model.ItemBase, style *model.ChartStyle) graphics.Rect {
	return graphics.Rect{
		X: float64(base.X),
		Y: float64(base.Y),
		W: float64(style.Width),
		H: float64(style.Height),
	}
}

// insetForStroke shrinks a rectangle by half a pixel so a one-pixel border
// lands on whole pixels instead of straddling two columns at half opacity
// each. InfoPanel applies the same offset.
func insetForStroke(r graphics.Rect) graphics.Rect {
	return graphics.Rect{X: r.X + 0.5, Y: r.Y + 0.5, W: r.W - 1, H: r.H - 1}
}

// chartRange returns the value range a chart plots over.
//
// With AutoValue the range tracks the data actually seen, so a sensor whose
// real span is far narrower than its nominal one still fills the chart. A
// degenerate range is widened rather than divided by.
func chartRange(style *model.ChartStyle, samples []float64) (min, max float64) {
	min, max = style.MinValue, style.MaxValue

	if style.AutoValue && len(samples) > 0 {
		min, max = samples[0], samples[0]
		for _, v := range samples[1:] {
			min = math.Min(min, v)
			max = math.Max(max, v)
		}
	}

	if max <= min {
		max = min + 1
	}
	return min, max
}

// normalize maps a value onto 0..1 across a range, clamped.
func normalize(v, min, max float64) float64 {
	if max == min {
		return 0
	}
	return math.Max(0, math.Min(1, (v-min)/(max-min)))
}

// drawGraph plots a sensor's recent history as a line or histogram.
func drawGraph(g *graphics.Graphics, item *model.GraphItem, _ *model.EvalContext, frame *Frame) {
	area := chartArea(&item.ItemBase, &item.ChartStyle)
	if area.IsEmpty() {
		return
	}
	transform := graphics.Transform{Rotation: float64(item.Rotation)}

	if item.Background {
		g.FillRect(area, 0,
			graphics.SolidFill(graphics.ColorOr(item.BackgroundColor, color.NRGBA{255, 255, 255, 255})),
			transform)
	}

	// The trace is drawn before the border so the border frames it cleanly.
	if frame.History != nil {
		if samples, ok := frame.History.Values(item.Key, item.SampleCapacity()); ok && len(samples) > 0 {
			drawTrace(g, item, area, samples, transform)
		}
	}

	if item.Frame {
		g.StrokeRect(insetForStroke(area), 0,
			graphics.SolidStroke(graphics.ColorOr(item.FrameColor, color.NRGBA{0, 0, 0, 255}), 1),
			transform)
	}
}

// drawTrace paints a graph's samples inside its plot area.
func drawTrace(g *graphics.Graphics, item *model.GraphItem, area graphics.Rect,
	samples []float64, transform graphics.Transform) {

	min, max := chartRange(&item.ChartStyle, samples)

	step := float64(item.Step)
	if step <= 0 {
		step = 1
	}

	// Samples run right-to-left so the newest sits at the leading edge and the
	// trace scrolls the way a live graph should. FlipX reverses that.
	points := make([]graphics.Point, 0, len(samples))
	for i, v := range samples {
		offset := float64(len(samples)-1-i) * step
		x := area.X + area.W - offset
		if item.FlipX {
			x = area.X + offset
		}
		if x < area.X-step || x > area.X+area.W+step {
			continue // scrolled off the chart
		}
		points = append(points, graphics.Point{
			X: x,
			Y: area.Y + area.H - normalize(v, min, max)*area.H,
		})
	}
	if len(points) == 0 {
		return
	}

	traceColor := graphics.ColorOr(item.Color, color.NRGBA{128, 128, 128, 255})

	g.Clip(area, func() {
		if item.Type == model.GraphHistogram {
			drawHistogram(g, area, points, item, traceColor, transform)
			return
		}

		if item.Fill && len(points) >= 2 {
			// Close the trace down to the baseline to shade the area beneath.
			filled := make([]graphics.Point, 0, len(points)+2)
			filled = append(filled, graphics.Point{X: points[0].X, Y: area.Y + area.H})
			filled = append(filled, points...)
			filled = append(filled, graphics.Point{X: points[len(points)-1].X, Y: area.Y + area.H})

			g.Polygon(filled,
				graphics.SolidFill(graphics.ColorOr(item.FillColor, color.NRGBA{60, 136, 141, 255})),
				graphics.Stroke{}, transform)
		}

		thickness := float64(item.Thickness)
		if thickness <= 0 {
			thickness = 1
		}
		g.Polyline(points, graphics.SolidStroke(traceColor, thickness), transform)
	})
}

// drawHistogram paints one vertical bar per sample.
func drawHistogram(g *graphics.Graphics, area graphics.Rect, points []graphics.Point,
	item *model.GraphItem, traceColor color.NRGBA, transform graphics.Transform) {

	width := float64(item.Thickness)
	if width <= 0 {
		width = math.Max(1, float64(item.Step)-1)
	}

	fill := graphics.SolidFill(traceColor)
	for _, p := range points {
		bar := graphics.Rect{X: p.X - width/2, Y: p.Y, W: width, H: area.Y + area.H - p.Y}
		if bar.H <= 0 {
			continue
		}
		g.FillRect(bar, 0, fill, transform)
	}
}

// drawBar fills a rounded track in proportion to its sensor value.
func drawBar(g *graphics.Graphics, item *model.BarItem, ctx *model.EvalContext, frame *Frame) {
	area := chartArea(&item.ItemBase, &item.ChartStyle)
	if area.IsEmpty() {
		return
	}

	transform := graphics.Transform{Rotation: float64(item.Rotation)}
	radius := float64(item.CornerRadius)

	if item.Background {
		g.FillRect(area, radius,
			graphics.SolidFill(graphics.ColorOr(item.BackgroundColor, color.NRGBA{255, 255, 255, 255})),
			transform)
	}

	fraction, ok := item.Normalized(ctx, item.MinValue, item.MaxValue)
	if ok {
		fraction = frame.smooth(item.ID, fraction)
	}

	if ok && fraction > 0 {
		filled := area.W * fraction
		bar := graphics.Rect{X: area.X, Y: area.Y, W: filled, H: area.H}
		if item.FlipX {
			bar.X = area.X + area.W - filled // fill from the right edge inward
		}

		fill := graphics.Fill{
			Enabled:       true,
			Color:         graphics.ColorOr(item.Color, color.NRGBA{128, 128, 128, 255}),
			Gradient:      item.Gradient,
			GradientColor: graphics.ColorOr(item.GradientColor, color.NRGBA{59, 59, 59, 255}),
			Kind:          graphics.GradientLinear,
		}

		if fill.Gradient {
			// Project the gradient across the whole track and clip to the
			// filled part, so its colors stay put as the value changes rather
			// than compressing toward the fill edge.
			g.Clip(bar, func() {
				g.FillRect(area, radius, fill, transform)
			})
		} else {
			g.FillRect(bar, radius, fill, transform)
		}
	}

	if item.Frame {
		g.StrokeRect(insetForStroke(area), math.Max(0, radius-0.5),
			graphics.SolidStroke(graphics.ColorOr(item.FrameColor, color.NRGBA{0, 0, 0, 255}), 1),
			transform)
	}
}

// drawDonut fills an arc in proportion to its sensor value.
//
// A donut has no backing rectangle: its Background property colors the
// unfilled remainder of the ring, and its Frame outlines the ring's edges.
func drawDonut(g *graphics.Graphics, item *model.DonutItem, ctx *model.EvalContext, frame *Frame) {
	area := chartArea(&item.ItemBase, &item.ChartStyle)
	if area.IsEmpty() {
		return
	}

	fraction, ok := item.Normalized(ctx, item.MinValue, item.MaxValue)
	if !ok {
		return
	}
	fraction = frame.smooth(item.ID, fraction)

	track := color.NRGBA{}
	if item.Background {
		track = graphics.ColorOr(item.BackgroundColor, color.NRGBA{})
	}

	stroke := graphics.Stroke{}
	if item.Frame {
		stroke = graphics.SolidStroke(
			graphics.ColorOr(item.FrameColor, color.NRGBA{0, 0, 0, 255}), 1)
	}

	// Inset by a pixel so the ring's outer edge and its outline stay inside
	// the item's declared bounds.
	const inset = 1
	cx, cy := area.Center()

	g.Donut(graphics.Point{X: cx, Y: cy},
		float64(item.Radius())-inset, float64(item.Thickness),
		fraction, float64(item.Span),
		graphics.ColorOr(item.Color, color.NRGBA{128, 128, 128, 255}),
		track, stroke,
		graphics.Transform{Rotation: float64(item.Rotation)})
}
