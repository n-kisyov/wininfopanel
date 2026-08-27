package graphics

import (
	"image/color"
	"math"

	"github.com/fogleman/gg"
)

// GradientKind selects how a gradient is projected across a shape.
type GradientKind string

const (
	// GradientLinear runs along a direction set by an angle.
	GradientLinear GradientKind = "linear"
	// GradientRadial runs outward from the shape's center.
	GradientRadial GradientKind = "radial"
)

// Fill describes how to paint a shape's interior.
//
// The zero value paints nothing, so a shape with fill disabled needs no
// special case at the call site.
type Fill struct {
	Color color.NRGBA
	// Gradient, when set, blends Color into GradientColor across the shape.
	Gradient      bool
	GradientColor color.NRGBA
	Kind          GradientKind
	// Angle orients a linear gradient in degrees: 0 runs left to right, 90 top
	// to bottom.
	Angle float64
	// Enabled must be true for anything to be painted.
	Enabled bool
}

// SolidFill returns an opaque single-color fill.
func SolidFill(c color.NRGBA) Fill {
	return Fill{Color: c, Enabled: true}
}

// Stroke describes how to paint a shape's outline.
type Stroke struct {
	Color   color.NRGBA
	Width   float64
	Enabled bool
}

// SolidStroke returns a single-color outline.
func SolidStroke(c color.NRGBA, width float64) Stroke {
	return Stroke{Color: c, Width: width, Enabled: width > 0}
}

// applyFill sets the current paint source from a fill description, scaled by
// opacity. It reports whether anything should be painted.
func (g *Graphics) applyFill(f Fill, bounds Rect, opacity float64) bool {
	if !f.Enabled {
		return false
	}

	base := WithAlpha(f.Color, opacity)
	if !f.Gradient {
		g.ctx.SetColor(base)
		return true
	}

	end := WithAlpha(f.GradientColor, opacity)
	if f.Kind == GradientRadial {
		cx, cy := bounds.Center()
		radius := math.Max(bounds.W, bounds.H) / 2
		grad := gg.NewRadialGradient(cx, cy, 0, cx, cy, radius)
		grad.AddColorStop(0, base)
		grad.AddColorStop(1, end)
		g.ctx.SetFillStyle(grad)
		return true
	}

	x0, y0, x1, y1 := gradientEndpoints(bounds, f.Angle)
	grad := gg.NewLinearGradient(x0, y0, x1, y1)
	grad.AddColorStop(0, base)
	grad.AddColorStop(1, end)
	g.ctx.SetFillStyle(grad)
	return true
}

// gradientEndpoints projects a gradient angle onto a rectangle, returning the
// start and end points of the gradient axis.
//
// The axis passes through the rectangle's center, so rotating the angle spins
// the gradient in place rather than sliding it off the shape.
func gradientEndpoints(bounds Rect, angleDegrees float64) (x0, y0, x1, y1 float64) {
	cx, cy := bounds.Center()

	radians := gg.Radians(angleDegrees)
	dx, dy := math.Cos(radians), math.Sin(radians)

	// Half the rectangle's extent projected onto the gradient direction: this
	// is how far the axis must reach for the gradient to span the shape.
	reach := math.Abs(dx)*bounds.W/2 + math.Abs(dy)*bounds.H/2

	return cx - dx*reach, cy - dy*reach, cx + dx*reach, cy + dy*reach
}

// FillRect paints a rectangle, optionally with rounded corners.
func (g *Graphics) FillRect(r Rect, cornerRadius float64, f Fill, t Transform) {
	if r.IsEmpty() {
		return
	}

	cx, cy := r.Center()
	g.withRotation(t.Rotation, cx, cy, func() {
		if !g.applyFill(f, r, t.alpha()) {
			return
		}
		g.pathRect(r, cornerRadius)
		g.ctx.Fill()
	})
}

// StrokeRect outlines a rectangle, optionally with rounded corners.
func (g *Graphics) StrokeRect(r Rect, cornerRadius float64, s Stroke, t Transform) {
	if r.IsEmpty() || !s.Enabled || s.Width <= 0 {
		return
	}

	cx, cy := r.Center()
	g.withRotation(t.Rotation, cx, cy, func() {
		g.ctx.SetColor(WithAlpha(s.Color, t.alpha()))
		g.ctx.SetLineWidth(s.Width)
		g.pathRect(r, cornerRadius)
		g.ctx.Stroke()
	})
}

// pathRect adds a rectangle to the current path, rounding corners when asked.
func (g *Graphics) pathRect(r Rect, cornerRadius float64) {
	// A radius larger than half the shorter side would produce self-crossing
	// corners, so cap it at the point where the shape becomes a capsule.
	maxRadius := math.Min(r.W, r.H) / 2
	radius := math.Min(cornerRadius, maxRadius)

	if radius <= 0 {
		g.ctx.DrawRectangle(r.X, r.Y, r.W, r.H)
		return
	}
	g.ctx.DrawRoundedRectangle(r.X, r.Y, r.W, r.H, radius)
}

// FillEllipse paints an ellipse inscribed in the given rectangle.
func (g *Graphics) FillEllipse(r Rect, f Fill, t Transform) {
	if r.IsEmpty() {
		return
	}

	cx, cy := r.Center()
	g.withRotation(t.Rotation, cx, cy, func() {
		if !g.applyFill(f, r, t.alpha()) {
			return
		}
		g.ctx.DrawEllipse(cx, cy, r.W/2, r.H/2)
		g.ctx.Fill()
	})
}

// StrokeEllipse outlines an ellipse inscribed in the given rectangle.
func (g *Graphics) StrokeEllipse(r Rect, s Stroke, t Transform) {
	if r.IsEmpty() || !s.Enabled || s.Width <= 0 {
		return
	}

	cx, cy := r.Center()
	g.withRotation(t.Rotation, cx, cy, func() {
		g.ctx.SetColor(WithAlpha(s.Color, t.alpha()))
		g.ctx.SetLineWidth(s.Width)
		g.ctx.DrawEllipse(cx, cy, r.W/2, r.H/2)
		g.ctx.Stroke()
	})
}

// Line draws a straight segment.
func (g *Graphics) Line(x0, y0, x1, y1 float64, s Stroke, t Transform) {
	if !s.Enabled || s.Width <= 0 {
		return
	}

	cx, cy := (x0+x1)/2, (y0+y1)/2
	g.withRotation(t.Rotation, cx, cy, func() {
		g.ctx.SetColor(WithAlpha(s.Color, t.alpha()))
		g.ctx.SetLineWidth(s.Width)
		g.ctx.DrawLine(x0, y0, x1, y1)
		g.ctx.Stroke()
	})
}

// Polyline draws a connected sequence of segments. Fewer than two points draws
// nothing.
func (g *Graphics) Polyline(points []Point, s Stroke, t Transform) {
	if len(points) < 2 || !s.Enabled || s.Width <= 0 {
		return
	}

	bounds := boundsOf(points)
	cx, cy := bounds.Center()
	g.withRotation(t.Rotation, cx, cy, func() {
		g.ctx.SetColor(WithAlpha(s.Color, t.alpha()))
		g.ctx.SetLineWidth(s.Width)
		g.ctx.MoveTo(points[0].X, points[0].Y)
		for _, p := range points[1:] {
			g.ctx.LineTo(p.X, p.Y)
		}
		g.ctx.Stroke()
	})
}

// Polygon paints a closed shape through the given points.
func (g *Graphics) Polygon(points []Point, f Fill, s Stroke, t Transform) {
	if len(points) < 3 {
		return
	}

	bounds := boundsOf(points)
	cx, cy := bounds.Center()
	opacity := t.alpha()

	g.withRotation(t.Rotation, cx, cy, func() {
		trace := func() {
			g.ctx.MoveTo(points[0].X, points[0].Y)
			for _, p := range points[1:] {
				g.ctx.LineTo(p.X, p.Y)
			}
			g.ctx.ClosePath()
		}

		if g.applyFill(f, bounds, opacity) {
			trace()
			g.ctx.Fill()
		}
		if s.Enabled && s.Width > 0 {
			g.ctx.SetColor(WithAlpha(s.Color, opacity))
			g.ctx.SetLineWidth(s.Width)
			trace()
			g.ctx.Stroke()
		}
	})
}

// Point is a position on the surface.
type Point struct {
	X, Y float64
}

// boundsOf returns the axis-aligned extent of a point set.
func boundsOf(points []Point) Rect {
	if len(points) == 0 {
		return Rect{}
	}

	minX, minY := points[0].X, points[0].Y
	maxX, maxY := minX, minY
	for _, p := range points[1:] {
		minX = math.Min(minX, p.X)
		minY = math.Min(minY, p.Y)
		maxX = math.Max(maxX, p.X)
		maxY = math.Max(maxY, p.Y)
	}
	return Rect{X: minX, Y: minY, W: maxX - minX, H: maxY - minY}
}

// Donut paints a ring filled clockwise in proportion to a value.
//
// The arc starts at the top and sweeps span degrees; a span below 360 leaves a
// gap, which is how a gauge-style indicator is drawn. Background is painted
// under the full span so the unfilled remainder stays visible.
func (g *Graphics) Donut(center Point, radius, thickness float64, progress float64,
	span float64, fg, bg color.NRGBA, s Stroke, t Transform) {

	if radius <= 0 || thickness <= 0 {
		return
	}
	if span <= 0 || span > 360 {
		span = 360
	}

	opacity := t.alpha()
	// The arc is stroked along the ring's midline, so its radius is inset by
	// half the thickness for the band to land inside the requested radius.
	arcRadius := radius - thickness/2
	if arcRadius <= 0 {
		arcRadius = radius / 2
		thickness = radius
	}

	// Start at the top and sweep clockwise, centering a partial span so a
	// 270-degree gauge is symmetric about vertical.
	start := -math.Pi/2 - gg.Radians(span)/2
	if span >= 360 {
		start = -math.Pi / 2
	}
	sweep := gg.Radians(span)

	g.withRotation(t.Rotation, center.X, center.Y, func() {
		g.ctx.SetLineWidth(thickness)
		g.ctx.SetLineCapButt()

		if bg.A > 0 {
			g.ctx.SetColor(WithAlpha(bg, opacity))
			g.ctx.DrawArc(center.X, center.Y, arcRadius, start, start+sweep)
			g.ctx.Stroke()
		}

		if filled := clamp01(progress); filled > 0 {
			g.ctx.SetColor(WithAlpha(fg, opacity))
			g.ctx.DrawArc(center.X, center.Y, arcRadius, start, start+sweep*filled)
			g.ctx.Stroke()
		}

		if s.Enabled && s.Width > 0 {
			// The outline traces the ring's outer and inner edges, not the
			// stroked band itself.
			g.ctx.SetColor(WithAlpha(s.Color, opacity))
			g.ctx.SetLineWidth(s.Width)
			g.ctx.DrawArc(center.X, center.Y, radius, start, start+sweep)
			g.ctx.Stroke()
			g.ctx.DrawArc(center.X, center.Y, radius-thickness, start, start+sweep)
			g.ctx.Stroke()
		}
	})
}
