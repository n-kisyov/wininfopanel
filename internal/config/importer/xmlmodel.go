package importer

import "encoding/xml"

// InfoPanel serializes its display items with .NET's XmlSerializer using an
// extra-types list, which discriminates concrete types by an xsi:type
// attribute on a shared <DisplayItem> element:
//
//	<ArrayOfDisplayItem>
//	  <DisplayItem xsi:type="SensorDisplayItem">
//	    <Name>CPU</Name><X>10</X><Y>20</Y>...
//
// Rather than one Go struct per item type, everything is decoded into a single
// permissive struct holding the union of every field. The types share most of
// their fields anyway, and a field absent from a given item simply stays at its
// zero value -- which makes the importer tolerant of the schema differences
// between InfoPanel versions, where a strict per-type mapping would fail
// outright on an unexpected element.

// xmlProfileList is the root of profiles.xml.
type xmlProfileList struct {
	XMLName  xml.Name     `xml:"ArrayOfProfile"`
	Profiles []xmlProfile `xml:"Profile"`
}

// xmlProfile mirrors InfoPanel's Profile.
type xmlProfile struct {
	Guid            string  `xml:"Guid"`
	Name            string  `xml:"Name"`
	Width           int     `xml:"Width"`
	Height          int     `xml:"Height"`
	BackgroundColor string  `xml:"BackgroundColor"`
	Font            string  `xml:"Font"`
	FontSize        int     `xml:"FontSize"`
	Color           string  `xml:"Color"`
	FontScale       float64 `xml:"FontScale"`

	Active  bool `xml:"Active"`
	Topmost bool `xml:"Topmost"`
	Drag    bool `xml:"Drag"`
	Resize  bool `xml:"Resize"`
	ShowFps bool `xml:"ShowFps"`
	OpenGL  bool `xml:"OpenGL"`

	WindowX int `xml:"WindowX"`
	WindowY int `xml:"WindowY"`

	TargetWindow *xmlTargetWindow `xml:"TargetWindow"`

	TriggerProcessNames  string `xml:"TriggerProcessNames"`
	StrictWindowMatching bool   `xml:"StrictWindowMatching"`
}

type xmlTargetWindow struct {
	X          int    `xml:"X"`
	Y          int    `xml:"Y"`
	Width      int    `xml:"Width"`
	Height     int    `xml:"Height"`
	DeviceName string `xml:"DeviceName"`
}

// xmlItemList is the root of a profiles/{guid}.xml layout file.
type xmlItemList struct {
	XMLName xml.Name  `xml:"ArrayOfDisplayItem"`
	Items   []xmlItem `xml:"DisplayItem"`
}

// xmlItem is the union of every display item type's fields.
type xmlItem struct {
	// Type carries the xsi:type attribute naming the concrete class.
	Type string `xml:"http://www.w3.org/2001/XMLSchema-instance type,attr"`

	// Shared by every item. InfoPanel marks Guid as XmlIgnore, so item
	// identity is not persisted and fresh IDs are assigned on import.
	Name     string `xml:"Name"`
	X        int    `xml:"X"`
	Y        int    `xml:"Y"`
	Rotation int    `xml:"Rotation"`
	Hidden   bool   `xml:"Hidden"`
	IsLocked bool   `xml:"IsLocked"`

	// Typography, shared by the text-derived items.
	Font      string `xml:"Font"`
	FontStyle string `xml:"FontStyle"`
	FontSize  int    `xml:"FontSize"`
	Bold      bool   `xml:"Bold"`
	Italic    bool   `xml:"Italic"`
	Underline bool   `xml:"Underline"`
	Strikeout bool   `xml:"Strikeout"`
	Color     string `xml:"Color"`

	RightAlign  bool `xml:"RightAlign"`
	CenterAlign bool `xml:"CenterAlign"`
	Uppercase   bool `xml:"Uppercase"`
	Wrap        bool `xml:"Wrap"`
	Ellipsis    bool `xml:"Ellipsis"`

	Width  int `xml:"Width"`
	Height int `xml:"Height"`

	Marquee        bool `xml:"Marquee"`
	MarqueeSpeed   int  `xml:"MarqueeSpeed"`
	MarqueeSpacing int  `xml:"MarqueeSpacing"`

	GlowEnabled   bool   `xml:"GlowEnabled"`
	GlowRadius    int    `xml:"GlowRadius"`
	GlowColor     string `xml:"GlowColor"`
	GlowBlendMode string `xml:"GlowBlendMode"`

	// Clock and calendar.
	Format string `xml:"Format"`

	// Sensor binding, shared by sensor readouts, charts, gauges, and tables.
	SensorName        string `xml:"SensorName"`
	SensorType        string `xml:"SensorType"`
	HwInfoRemoteIndex int    `xml:"HwInfoRemoteIndex"`
	Id                uint32 `xml:"Id"`
	Instance          uint32 `xml:"Instance"`
	EntryId           uint32 `xml:"EntryId"`
	LibreSensorId     string `xml:"LibreSensorId"`
	PluginSensorId    string `xml:"PluginSensorId"`
	ValueType         string `xml:"ValueType"`

	// Sensor readout formatting.
	Threshold1             float64 `xml:"Threshold1"`
	Threshold1Color        string  `xml:"Threshold1Color"`
	Threshold2             float64 `xml:"Threshold2"`
	Threshold2Color        string  `xml:"Threshold2Color"`
	ShowName               bool    `xml:"ShowName"`
	Unit                   string  `xml:"Unit"`
	OverrideUnit           bool    `xml:"OverrideUnit"`
	ShowUnit               bool    `xml:"ShowUnit"`
	OverridePrecision      bool    `xml:"OverridePrecision"`
	Precision              int     `xml:"Precision"`
	ShowThousandsSeparator bool    `xml:"ShowThousandsSeparator"`

	// Value transform. InfoPanel names these after the arithmetic rather than
	// the intent.
	AdditionModifier       float64 `xml:"AdditionModifier"`
	AbsoluteAddition       bool    `xml:"AbsoluteAddition"`
	MultiplicationModifier float64 `xml:"MultiplicationModifier"`
	DivisionToggle         bool    `xml:"DivisionToggle"`

	// Charts.
	MinValue        float64 `xml:"MinValue"`
	MaxValue        float64 `xml:"MaxValue"`
	AutoValue       bool    `xml:"AutoValue"`
	FlipX           bool    `xml:"FlipX"`
	Frame           bool    `xml:"Frame"`
	FrameColor      string  `xml:"FrameColor"`
	Background      bool    `xml:"Background"`
	BackgroundColor string  `xml:"BackgroundColor"`

	// Graph.
	GraphType string `xml:"Type"`
	Thickness int    `xml:"Thickness"`
	Step      int    `xml:"Step"`
	Fill      bool   `xml:"Fill"`
	FillColor string `xml:"FillColor"`

	// Bar.
	CornerRadius  int    `xml:"CornerRadius"`
	Gradient      bool   `xml:"Gradient"`
	GradientColor string `xml:"GradientColor"`

	// Donut.
	Span int `xml:"Span"`

	// Images. The element named Type is shared with the graph's type, so both
	// are read from the same field and interpreted per item kind.
	FilePath        string `xml:"FilePath"`
	RelativePath    bool   `xml:"RelativePath"`
	RtspUrl         string `xml:"RtspUrl"`
	HttpUrl         string `xml:"HttpUrl"`
	Volume          int    `xml:"Volume"`
	Cache           bool   `xml:"Cache"`
	PersistentCache bool   `xml:"PersistentCache"`
	Scale           int    `xml:"Scale"`
	Layer           bool   `xml:"Layer"`
	LayerColor      string `xml:"LayerColor"`
	ShowPanel       bool   `xml:"ShowPanel"`

	// Gauge.
	AnimationSpeed float64      `xml:"AnimationSpeed"`
	Images         xmlImageList `xml:"Images"`

	// Shape. InfoPanel writes the shape kind to the same Type element.
	Fill2       bool   `xml:"FillShape"`
	StrokeColor string `xml:"StrokeColor"`
	StrokeWidth int    `xml:"StrokeWidth"`

	// Group.
	DisplayItems xmlItemList2 `xml:"DisplayItems"`

	// Table.
	TableFormat string `xml:"TableFormat"`
	MaxRows     int    `xml:"MaxRows"`
}

// xmlImageList holds a gauge's frames.
type xmlImageList struct {
	Items []xmlItem `xml:"ImageDisplayItem"`
}

// xmlItemList2 holds a group's children. It is a distinct type from
// xmlItemList because a nested list has no ArrayOfDisplayItem root element.
type xmlItemList2 struct {
	Items []xmlItem `xml:"DisplayItem"`
}
