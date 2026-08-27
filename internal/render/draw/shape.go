package draw

import (
	"image/color"
	"math"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
)

// drawShape paints one of the twelve geometric primitives.
//
// Rectangles, rounded rectangles, and ellipses use the surface's own
// primitives; everything else is expressed as a polygon, which keeps one code
// path for fill, stroke, gradient, and rotation across all of them.
func drawShape(g *graphics.Graphics, item *model.ShapeItem, _ *Frame) {
	area := graphics.Rect{
		X: float64(item.X),
		Y: float64(item.Y),
		W: float64(item.Width),
		H: float64(item.Height),
	}
	if area.IsEmpty() {
		return
	}

	fill := graphics.Fill{
		Enabled:       item.Fill,
		Color:         graphics.ColorOr(item.FillColor, color.NRGBA{128, 128, 128, 255}),
		Gradient:      item.Gradient,
		GradientColor: graphics.ColorOr(item.GradientColor, color.NRGBA{59, 59, 59, 255}),
		Kind:          gradientKind(item.GradientType),
		Angle:         item.GradientAngle,
	}
	stroke := graphics.Stroke{
		Enabled: item.Stroke && item.StrokeWidth > 0,
		Color:   graphics.ColorOr(item.StrokeColor, color.NRGBA{0, 0, 0, 255}),
		Width:   float64(item.StrokeWidth),
	}
	transform := graphics.Transform{Rotation: float64(item.Rotation)}

	switch item.Type {
	case model.ShapeRectangle:
		g.FillRect(area, float64(item.CornerRadius), fill, transform)
		g.StrokeRect(area, float64(item.CornerRadius), stroke, transform)

	case model.ShapeCapsule:
		// A capsule is a rectangle rounded to the limit, so the radius is half
		// the shorter side.
		radius := math.Min(area.W, area.H) / 2
		g.FillRect(area, radius, fill, transform)
		g.StrokeRect(area, radius, stroke, transform)

	case model.ShapeEllipse:
		g.FillEllipse(area, fill, transform)
		g.StrokeEllipse(area, stroke, transform)

	default:
		g.Polygon(shapePoints(item.Type, area), fill, stroke, transform)
	}
}

func gradientKind(t model.GradientType) graphics.GradientKind {
	if t == model.GradientRadial {
		return graphics.GradientRadial
	}
	return graphics.GradientLinear
}

// shapePoints returns the outline of a polygonal shape inscribed in area.
//
// Every shape is built in normalized 0..1 space and then mapped onto the
// rectangle, so all of them stretch consistently and none needs its own
// scaling arithmetic.
func shapePoints(shape model.ShapeType, area graphics.Rect) []graphics.Point {
	var unit []graphics.Point

	switch shape {
	case model.ShapeTriangle:
		unit = []graphics.Point{{X: 0.5, Y: 0}, {X: 1, Y: 1}, {X: 0, Y: 1}}

	case model.ShapeTrapezoid:
		unit = []graphics.Point{{X: 0.25, Y: 0}, {X: 0.75, Y: 0}, {X: 1, Y: 1}, {X: 0, Y: 1}}

	case model.ShapeParallelogram:
		unit = []graphics.Point{{X: 0.25, Y: 0}, {X: 1, Y: 0}, {X: 0.75, Y: 1}, {X: 0, Y: 1}}

	case model.ShapePentagon:
		unit = regularPolygon(5)

	case model.ShapeHexagon:
		unit = regularPolygon(6)

	case model.ShapeOctagon:
		unit = regularPolygon(8)

	case model.ShapeStar:
		unit = starPolygon(5, 0.4)

	case model.ShapePlus:
		// A cross whose arms are one third of the extent.
		const a, b = 1.0 / 3, 2.0 / 3
		unit = []graphics.Point{
			{X: a, Y: 0}, {X: b, Y: 0}, {X: b, Y: a}, {X: 1, Y: a},
			{X: 1, Y: b}, {X: b, Y: b}, {X: b, Y: 1}, {X: a, Y: 1},
			{X: a, Y: b}, {X: 0, Y: b}, {X: 0, Y: a}, {X: a, Y: a},
		}

	case model.ShapeArrow:
		// A right-pointing arrow: a shaft with a triangular head.
		unit = []graphics.Point{
			{X: 0, Y: 0.3}, {X: 0.6, Y: 0.3}, {X: 0.6, Y: 0},
			{X: 1, Y: 0.5},
			{X: 0.6, Y: 1}, {X: 0.6, Y: 0.7}, {X: 0, Y: 0.7},
		}

	default:
		return nil
	}

	out := make([]graphics.Point, len(unit))
	for i, p := range unit {
		out[i] = graphics.Point{
			X: area.X + p.X*area.W,
			Y: area.Y + p.Y*area.H,
		}
	}
	return out
}

// regularPolygon returns the vertices of an n-sided polygon inscribed in the
// unit square, first vertex at the top.
func regularPolygon(sides int) []graphics.Point {
	out := make([]graphics.Point, sides)
	step := 2 * math.Pi / float64(sides)

	for i := range out {
		angle := -math.Pi/2 + float64(i)*step
		// Map the unit circle onto the unit square.
		out[i] = graphics.Point{
			X: 0.5 + 0.5*math.Cos(angle),
			Y: 0.5 + 0.5*math.Sin(angle),
		}
	}
	return out
}

// starPolygon returns a star with the given number of points, alternating
// between the outer edge and innerRatio of it.
func starPolygon(points int, innerRatio float64) []graphics.Point {
	out := make([]graphics.Point, 0, points*2)
	step := math.Pi / float64(points)

	for i := 0; i < points*2; i++ {
		radius := 0.5
		if i%2 == 1 {
			radius = 0.5 * innerRatio
		}
		angle := -math.Pi/2 + float64(i)*step
		out = append(out, graphics.Point{
			X: 0.5 + radius*math.Cos(angle),
			Y: 0.5 + radius*math.Sin(angle),
		})
	}
	return out
}
