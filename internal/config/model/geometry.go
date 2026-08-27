package model

import "math"

// Size is a width/height pair in device-independent pixels.
type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Rect is an axis-aligned rectangle.
type Rect struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Right  float64 `json:"right"`
	Bottom float64 `json:"bottom"`
}

// RectFromSize builds a rectangle at (x, y) with the given size.
func RectFromSize(x, y float64, s Size) Rect {
	return Rect{Left: x, Top: y, Right: x + s.Width, Bottom: y + s.Height}
}

// Width returns the rectangle's width.
func (r Rect) Width() float64 { return r.Right - r.Left }

// Height returns the rectangle's height.
func (r Rect) Height() float64 { return r.Bottom - r.Top }

// MidX returns the horizontal center.
func (r Rect) MidX() float64 { return (r.Left + r.Right) / 2 }

// MidY returns the vertical center.
func (r Rect) MidY() float64 { return (r.Top + r.Bottom) / 2 }

// Size returns the rectangle's extent.
func (r Rect) Size() Size { return Size{Width: r.Width(), Height: r.Height()} }

// Inflate grows the rectangle by dx on each horizontal edge and dy on each
// vertical edge.
func (r Rect) Inflate(dx, dy float64) Rect {
	return Rect{Left: r.Left - dx, Top: r.Top - dy, Right: r.Right + dx, Bottom: r.Bottom + dy}
}

// Union returns the smallest rectangle containing both r and other.
func (r Rect) Union(other Rect) Rect {
	return Rect{
		Left:   math.Min(r.Left, other.Left),
		Top:    math.Min(r.Top, other.Top),
		Right:  math.Max(r.Right, other.Right),
		Bottom: math.Max(r.Bottom, other.Bottom),
	}
}

// IsEmpty reports whether the rectangle encloses no area.
func (r Rect) IsEmpty() bool { return r.Width() <= 0 || r.Height() <= 0 }

// Point is a position in profile coordinates.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// ContainsPoint reports whether p falls inside bounds after bounds is rotated
// by rotation degrees about its own center.
//
// The point is transformed into the rectangle's local (unrotated) frame by
// applying the inverse rotation, then tested against the axis-aligned extent —
// the same approach InfoPanel uses for design-canvas hit testing.
func ContainsPoint(bounds Rect, rotation int, p Point) bool {
	cx, cy := bounds.MidX(), bounds.MidY()

	radians := -float64(rotation) * math.Pi / 180.0 // negated: this is the inverse
	cos, sin := math.Cos(radians), math.Sin(radians)

	dx, dy := p.X-cx, p.Y-cy
	localX := dx*cos - dy*sin
	localY := dx*sin + dy*cos

	halfW, halfH := bounds.Width()/2, bounds.Height()/2
	return localX >= -halfW && localX <= halfW && localY >= -halfH && localY <= halfH
}
