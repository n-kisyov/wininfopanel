package model

// ShapeType names one of the primitive shapes a ShapeItem can draw.
type ShapeType string

const (
	// Four-sided shapes.
	ShapeRectangle     ShapeType = "rectangle"
	ShapeCapsule       ShapeType = "capsule"
	ShapeTrapezoid     ShapeType = "trapezoid"
	ShapeParallelogram ShapeType = "parallelogram"

	// Circular shapes.
	ShapeEllipse ShapeType = "ellipse"

	// Polygons, by side count.
	ShapeTriangle ShapeType = "triangle"
	ShapePentagon ShapeType = "pentagon"
	ShapeHexagon  ShapeType = "hexagon"
	ShapeOctagon  ShapeType = "octagon"

	// Symbols.
	ShapeStar  ShapeType = "star"
	ShapePlus  ShapeType = "plus"
	ShapeArrow ShapeType = "arrow"
)

// ShapeTypes lists every shape, in the order the design UI presents them.
var ShapeTypes = []ShapeType{
	ShapeRectangle, ShapeCapsule, ShapeTrapezoid, ShapeParallelogram,
	ShapeEllipse,
	ShapeTriangle, ShapePentagon, ShapeHexagon, ShapeOctagon,
	ShapeStar, ShapePlus, ShapeArrow,
}

// GradientType selects how a gradient is projected across a shape.
type GradientType string

const (
	GradientLinear GradientType = "linear"
	GradientRadial GradientType = "radial"
)

// ShapeItem draws a filled and/or stroked geometric primitive.
type ShapeItem struct {
	ItemBase

	Type   ShapeType `json:"type"`
	Width  int       `json:"width"`
	Height int       `json:"height"`

	// CornerRadius rounds corners on shapes that support it.
	CornerRadius int `json:"cornerRadius,omitempty"`

	Fill      bool   `json:"fill"`
	FillColor string `json:"fillColor"`

	Stroke      bool   `json:"stroke,omitempty"`
	StrokeColor string `json:"strokeColor,omitempty"`
	StrokeWidth int    `json:"strokeWidth,omitempty"`

	// Gradient blends FillColor into GradientColor across the shape.
	Gradient      bool         `json:"gradient,omitempty"`
	GradientColor string       `json:"gradientColor,omitempty"`
	GradientType  GradientType `json:"gradientType,omitempty"`
	// GradientAngle orients a linear gradient, in degrees.
	GradientAngle float64 `json:"gradientAngle,omitempty"`
}

// NewShapeItem returns a shape of the given type.
func NewShapeItem(name string, t ShapeType) *ShapeItem {
	return &ShapeItem{
		ItemBase:      newItemBase(name),
		Type:          t,
		Width:         100,
		Height:        100,
		Fill:          true,
		FillColor:     "#808080",
		StrokeColor:   "#000000",
		StrokeWidth:   1,
		GradientColor: "#3B3B3B",
		GradientType:  GradientLinear,
		GradientAngle: 90,
	}
}

// Kind implements DisplayItem.
func (s *ShapeItem) Kind() ItemKind { return KindShape }

// Clone implements DisplayItem.
func (s *ShapeItem) Clone() DisplayItem {
	c := *s
	return &c
}

// Bounds implements DisplayItem.
//
// A stroke straddles the path, so half its width extends past the nominal box.
func (s *ShapeItem) Bounds(*EvalContext) Rect {
	r := RectFromSize(float64(s.X), float64(s.Y), Size{
		Width:  float64(s.Width),
		Height: float64(s.Height),
	})
	if s.Stroke && s.StrokeWidth > 0 {
		half := float64(s.StrokeWidth) / 2
		r = r.Inflate(half, half)
	}
	return r
}
