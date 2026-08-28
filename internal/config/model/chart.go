package model

// ChartStyle is the framing and sizing shared by graphs, bars, and donuts.
type ChartStyle struct {
	Width  int `json:"width"`
	Height int `json:"height"`

	// MinValue and MaxValue bound the plotted range.
	MinValue float64 `json:"minValue"`
	MaxValue float64 `json:"maxValue"`
	// AutoValue rescales the range to the data actually seen.
	AutoValue bool `json:"autoValue,omitempty"`

	// FlipX mirrors the chart horizontally, so a bar can fill right-to-left.
	FlipX bool `json:"flipX,omitempty"`

	Frame      bool   `json:"frame,omitempty"`
	FrameColor string `json:"frameColor,omitempty"`

	Background      bool   `json:"background,omitempty"`
	BackgroundColor string `json:"backgroundColor,omitempty"`

	Color string `json:"color"`
}

func defaultChartStyle() ChartStyle {
	return ChartStyle{
		Width:           400,
		Height:          50,
		MinValue:        0,
		MaxValue:        100,
		Frame:           true,
		FrameColor:      "#000000",
		Background:      true,
		BackgroundColor: "#FFFFFF",
		Color:           "#808080",
	}
}

func chartBounds(base *ItemBase, style *ChartStyle) Rect {
	return RectFromSize(float64(base.X), float64(base.Y), Size{
		Width:  float64(style.Width),
		Height: float64(style.Height),
	})
}

// GraphType selects how a graph plots its history.
type GraphType string

const (
	// GraphLine connects successive samples.
	GraphLine GraphType = "line"
	// GraphHistogram draws one vertical bar per sample.
	GraphHistogram GraphType = "histogram"
)

// GraphItem plots a sensor's recent history.
type GraphItem struct {
	ItemBase
	ChartStyle
	SensorBinding

	Type GraphType `json:"type"`
	// Thickness is the line width in pixels.
	Thickness int `json:"thickness"`
	// Step is the horizontal distance in pixels between samples, which sets
	// how much history fits in the chart's width.
	Step int `json:"step"`

	// Fill shades the area under the plot.
	Fill      bool   `json:"fill,omitempty"`
	FillColor string `json:"fillColor,omitempty"`
}

// NewGraphItem returns a graph of the given type.
func NewGraphItem(name string, t GraphType) *GraphItem {
	return &GraphItem{
		ItemBase:      newItemBase(name),
		ChartStyle:    defaultChartStyle(),
		SensorBinding: NewSensorBinding(),
		Type:          t,
		Thickness:     2,
		Step:          4,
		Fill:          true,
		FillColor:     "#3C888DFF",
	}
}

// Kind implements DisplayItem.
func (g *GraphItem) Kind() ItemKind { return KindGraph }

// Clone implements DisplayItem.
func (g *GraphItem) Clone() DisplayItem {
	c := *g
	return &c
}

// Bounds implements DisplayItem.
func (g *GraphItem) Bounds(*EvalContext) Rect { return chartBounds(&g.ItemBase, &g.ChartStyle) }

// SampleCapacity reports how many samples fit across the graph's width.
func (g *GraphItem) SampleCapacity() int {
	step := g.Step
	if step <= 0 {
		step = 1
	}
	n := g.Width/step + 1
	if n < 2 {
		n = 2
	}
	return n
}

// BarItem fills a rectangle in proportion to its sensor value.
type BarItem struct {
	ItemBase
	ChartStyle
	SensorBinding

	// CornerRadius rounds the bar's corners.
	CornerRadius int `json:"cornerRadius,omitempty"`
	// Gradient blends Color into GradientColor along the bar.
	Gradient      bool   `json:"gradient,omitempty"`
	GradientColor string `json:"gradientColor,omitempty"`
}

// NewBarItem returns a bar chart.
func NewBarItem(name string) *BarItem {
	return &BarItem{
		ItemBase:      newItemBase(name),
		ChartStyle:    defaultChartStyle(),
		SensorBinding: NewSensorBinding(),
		Gradient:      true,
		GradientColor: "#3B3B3B",
	}
}

// Kind implements DisplayItem.
func (b *BarItem) Kind() ItemKind { return KindBar }

// Clone implements DisplayItem.
func (b *BarItem) Clone() DisplayItem {
	c := *b
	return &c
}

// Bounds implements DisplayItem.
func (b *BarItem) Bounds(*EvalContext) Rect { return chartBounds(&b.ItemBase, &b.ChartStyle) }

// DonutItem fills an arc in proportion to its sensor value.
type DonutItem struct {
	ItemBase
	ChartStyle
	SensorBinding

	// Thickness is the ring's radial width in pixels.
	Thickness int `json:"thickness"`
	// Span is the arc's sweep in degrees; 360 is a full ring.
	Span int `json:"span"`

	// StrokeWidth outlines the ring.
	StrokeWidth int    `json:"strokeWidth,omitempty"`
	StrokeColor string `json:"strokeColor,omitempty"`
}

// NewDonutItem returns a donut chart.
func NewDonutItem(name string) *DonutItem {
	d := &DonutItem{
		ItemBase:      newItemBase(name),
		ChartStyle:    defaultChartStyle(),
		SensorBinding: NewSensorBinding(),
		Thickness:     10,
		Span:          360,
		StrokeColor:   "#000000",
	}
	d.Width = 100
	d.Height = 100
	return d
}

// Kind implements DisplayItem.
func (d *DonutItem) Kind() ItemKind { return KindDonut }

// Clone implements DisplayItem.
func (d *DonutItem) Clone() DisplayItem {
	c := *d
	return &c
}

// Radius is half the donut's smaller dimension.
func (d *DonutItem) Radius() int {
	r := d.Width
	if d.Height < r {
		r = d.Height
	}
	return r / 2
}

// Bounds implements DisplayItem.
func (d *DonutItem) Bounds(*EvalContext) Rect { return chartBounds(&d.ItemBase, &d.ChartStyle) }
