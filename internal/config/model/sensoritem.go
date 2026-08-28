package model

// Threshold recolors a sensor readout once its value reaches Value.
type Threshold struct {
	Value float64 `json:"value"`
	Color string  `json:"color"`
}

// SensorItem draws a live sensor reading as text.
//
// Formatting follows InfoPanel: apply the binding's transform, choose a
// precision (explicit override, else a per-unit default), optionally group
// thousands, then append the unit and prepend the name.
type SensorItem struct {
	ItemBase
	TextStyle
	SensorBinding

	// Threshold1 and Threshold2 recolor the text above their values.
	// Threshold2 wins when both apply. A threshold of zero is inactive.
	Threshold1 Threshold `json:"threshold1,omitzero"`
	Threshold2 Threshold `json:"threshold2,omitzero"`

	// ShowName prefixes the reading with the item's name.
	ShowName bool `json:"showName,omitempty"`
	// ShowUnit appends the unit.
	ShowUnit bool `json:"showUnit"`
	// OverrideUnit substitutes Unit for the sensor's own unit.
	OverrideUnit bool   `json:"overrideUnit,omitempty"`
	Unit         string `json:"unit,omitempty"`

	// OverridePrecision replaces the per-unit default with Precision.
	OverridePrecision bool `json:"overridePrecision,omitempty"`
	Precision         int  `json:"precision,omitempty"`

	// ShowThousandsSeparator groups the integer part with commas.
	ShowThousandsSeparator bool `json:"showThousandsSeparator,omitempty"`
}

// NewSensorItem returns a sensor readout with InfoPanel's defaults.
func NewSensorItem(name string) *SensorItem {
	return &SensorItem{
		ItemBase:      newItemBase(name),
		TextStyle:     defaultTextStyle(),
		SensorBinding: NewSensorBinding(),
		ShowUnit:      true,
		Threshold1:    Threshold{Color: "#000000"},
		Threshold2:    Threshold{Color: "#000000"},
	}
}

// Kind implements DisplayItem.
func (s *SensorItem) Kind() ItemKind { return KindSensor }

// Clone implements DisplayItem.
func (s *SensorItem) Clone() DisplayItem {
	c := *s
	return &c
}

// EvaluateTextAndColor implements TextEvaluator.
//
// An unresolvable sensor renders as "-" in the item's base color, so a layout
// referencing a missing sensor stays legible rather than collapsing.
func (s *SensorItem) EvaluateTextAndColor(ctx *EvalContext) (string, string) {
	reading, ok := s.Read(ctx)
	if !ok {
		return s.applyCase("-"), s.Color
	}

	// String-valued sensors bypass numeric formatting entirely.
	if reading.Text != "" {
		return s.applyCase(s.decorate(reading.Text, reading.Unit)), s.colorFor(s.Apply(reading))
	}

	value := s.Apply(reading)

	precision := s.Precision
	floor := true
	if !s.OverridePrecision {
		precision = defaultPrecisionForUnit(reading.Unit)
		floor = false
	}

	text := formatValue(value, precision, floor)
	if s.ShowThousandsSeparator {
		text = applyThousandsSeparator(text)
	}

	return s.applyCase(s.decorate(text, reading.Unit)), s.colorFor(value)
}

// decorate appends the unit and prepends the name, per the item's settings.
func (s *SensorItem) decorate(value, sensorUnit string) string {
	if s.ShowUnit {
		if s.OverrideUnit {
			value += s.Unit
		} else {
			value += sensorUnit
		}
	}
	if s.ShowName {
		value = s.Name + " " + value
	}
	return value
}

// colorFor picks the threshold color a transformed value falls into.
func (s *SensorItem) colorFor(value float64) string {
	if s.Threshold2.Value > 0 && value >= s.Threshold2.Value {
		return s.Threshold2.Color
	}
	if s.Threshold1.Value > 0 && value >= s.Threshold1.Value {
		return s.Threshold1.Color
	}
	return s.Color
}

// Bounds implements DisplayItem.
func (s *SensorItem) Bounds(ctx *EvalContext) Rect {
	text, _ := s.EvaluateTextAndColor(ctx)
	return textBounds(&s.ItemBase, &s.TextStyle, text, ctx)
}

// TableItem renders a plugin-provided table.
//
// Format follows InfoPanel's "columnIndex:width|columnIndex:width" syntax,
// selecting which columns to show and how wide each is.
type TableItem struct {
	ItemBase
	TextStyle
	SensorBinding

	// Format selects and sizes columns, e.g. "0:150|1:60|2:80".
	Format string `json:"format,omitempty"`
	// MaxRows caps how many rows are drawn; 0 means all.
	MaxRows int `json:"maxRows,omitempty"`
	// ShowHeader draws the column header row.
	ShowHeader bool `json:"showHeader,omitempty"`
}

// NewTableItem returns a table readout.
func NewTableItem(name string) *TableItem {
	return &TableItem{
		ItemBase:      newItemBase(name),
		TextStyle:     defaultTextStyle(),
		SensorBinding: NewSensorBinding(),
		ShowHeader:    true,
	}
}

// Kind implements DisplayItem.
func (t *TableItem) Kind() ItemKind { return KindTable }

// Clone implements DisplayItem.
func (t *TableItem) Clone() DisplayItem {
	c := *t
	return &c
}

// Bounds implements DisplayItem.
//
// Table extent comes from the declared column widths and row count rather than
// from measuring content, so the box stays stable as values change.
func (t *TableItem) Bounds(ctx *EvalContext) Rect {
	width := float64(t.Width)
	if width == 0 {
		width = float64(TableFormatWidth(t.Format))
	}

	rows := t.MaxRows
	if rows <= 0 {
		rows = 1
	}
	if t.ShowHeader {
		rows++
	}

	lineHeight := ctx.measurer().Measure(t.spec("X", ctx.fontScale())).Height
	height := float64(t.Height)
	if height == 0 {
		height = lineHeight * float64(rows)
	}

	return RectFromSize(float64(t.X), float64(t.Y), Size{Width: width, Height: height})
}
