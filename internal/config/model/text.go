package model

import "strings"

// GlowSettings describes the optional soft halo drawn behind text.
type GlowSettings struct {
	Enabled bool `json:"enabled,omitempty"`
	// Radius is the blur radius in pixels.
	Radius int    `json:"radius,omitempty"`
	Color  string `json:"color,omitempty"`
	// BlendMode names a Porter-Duff or separable blend mode, e.g. "SrcOver".
	BlendMode string `json:"blendMode,omitempty"`
}

// TextStyle is the typography shared by every text-derived item.
type TextStyle struct {
	Font      string `json:"font,omitempty"`
	FontStyle string `json:"fontStyle,omitempty"`
	FontSize  int    `json:"fontSize"`
	Bold      bool   `json:"bold,omitempty"`
	Italic    bool   `json:"italic,omitempty"`
	Underline bool   `json:"underline,omitempty"`
	Strikeout bool   `json:"strikeout,omitempty"`
	Color     string `json:"color"`

	RightAlign  bool `json:"rightAlign,omitempty"`
	CenterAlign bool `json:"centerAlign,omitempty"`
	Uppercase   bool `json:"uppercase,omitempty"`
	Wrap        bool `json:"wrap,omitempty"`
	Ellipsis    bool `json:"ellipsis,omitempty"`

	// Width and Height bound the text box. Zero means size to content.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`

	// Marquee scrolls text horizontally when it overflows Width.
	Marquee bool `json:"marquee,omitempty"`
	// MarqueeSpeed is the scroll rate in pixels per second.
	MarqueeSpeed int `json:"marqueeSpeed,omitempty"`
	// MarqueeSpacing is the gap in pixels between repetitions.
	MarqueeSpacing int `json:"marqueeSpacing,omitempty"`

	Glow GlowSettings `json:"glow,omitzero"`
}

func defaultTextStyle() TextStyle {
	return TextStyle{
		FontSize:       20,
		Color:          "#000000",
		Wrap:           true,
		Ellipsis:       true,
		MarqueeSpeed:   50,
		MarqueeSpacing: 40,
		Glow:           GlowSettings{Radius: 8, Color: "#000000", BlendMode: "SrcOver"},
	}
}

// spec builds the measurement/draw description for a run of text in this style.
func (s *TextStyle) spec(text string, scale float64) TextSpec {
	return TextSpec{
		Text:      text,
		Font:      s.Font,
		FontStyle: s.FontStyle,
		FontSize:  s.FontSize,
		Bold:      s.Bold,
		Italic:    s.Italic,
		Underline: s.Underline,
		Strikeout: s.Strikeout,
		Wrap:      s.Wrap,
		Ellipsis:  s.Ellipsis,
		Width:     s.Width,
		Height:    s.Height,
		Scale:     scale,
	}
}

// textBounds computes an item's extent from its rendered text.
//
// Right-aligned unbounded text grows leftward from X, and an enabled glow
// inflates the box by its radius — both behaviors InfoPanel relies on for
// selection rectangles to line up with what is drawn.
func textBounds(base *ItemBase, style *TextStyle, text string, ctx *EvalContext) Rect {
	size := ctx.measurer().Measure(style.spec(text, ctx.fontScale()))

	x := float64(base.X)
	if style.Width == 0 && style.RightAlign {
		x -= size.Width
	}

	r := RectFromSize(x, float64(base.Y), size)
	if style.Glow.Enabled && style.Glow.Radius > 0 {
		r = r.Inflate(float64(style.Glow.Radius), float64(style.Glow.Radius))
	}
	return r
}

// applyCase uppercases text when the style asks for it.
func (s *TextStyle) applyCase(text string) string {
	if s.Uppercase {
		return strings.ToUpper(text)
	}
	return text
}

// TextItem draws a fixed string.
//
// As in InfoPanel, the drawn text is the item's Name: the design UI edits one
// field that serves as both label and content.
type TextItem struct {
	ItemBase
	TextStyle
}

// NewTextItem returns a text item displaying the given string.
func NewTextItem(text string) *TextItem {
	return &TextItem{ItemBase: newItemBase(text), TextStyle: defaultTextStyle()}
}

// Kind implements DisplayItem.
func (t *TextItem) Kind() ItemKind { return KindText }

// Clone implements DisplayItem.
func (t *TextItem) Clone() DisplayItem {
	c := *t
	return &c
}

// EvaluateTextAndColor implements TextEvaluator.
func (t *TextItem) EvaluateTextAndColor(*EvalContext) (string, string) {
	return t.applyCase(t.Name), t.Color
}

// Bounds implements DisplayItem.
func (t *TextItem) Bounds(ctx *EvalContext) Rect {
	text, _ := t.EvaluateTextAndColor(ctx)
	return textBounds(&t.ItemBase, &t.TextStyle, text, ctx)
}

// ClockItem draws the current time using a Go time layout.
type ClockItem struct {
	ItemBase
	TextStyle
	// Format is a Go reference-time layout, e.g. "03:04:05 PM".
	Format string `json:"format"`
}

// NewClockItem returns a clock showing 12-hour time with seconds.
func NewClockItem() *ClockItem {
	return &ClockItem{
		ItemBase:  newItemBase("Clock"),
		TextStyle: defaultTextStyle(),
		Format:    "03:04:05 PM",
	}
}

// Kind implements DisplayItem.
func (c *ClockItem) Kind() ItemKind { return KindClock }

// Clone implements DisplayItem.
func (c *ClockItem) Clone() DisplayItem {
	cp := *c
	return &cp
}

// EvaluateTextAndColor implements TextEvaluator.
func (c *ClockItem) EvaluateTextAndColor(ctx *EvalContext) (string, string) {
	return c.applyCase(ctx.now().Format(c.Format)), c.Color
}

// Bounds implements DisplayItem.
func (c *ClockItem) Bounds(ctx *EvalContext) Rect {
	text, _ := c.EvaluateTextAndColor(ctx)
	return textBounds(&c.ItemBase, &c.TextStyle, text, ctx)
}

// CalendarItem draws the current date using a Go time layout.
type CalendarItem struct {
	ItemBase
	TextStyle
	// Format is a Go reference-time layout, e.g. "02/01/2006".
	Format string `json:"format"`
}

// NewCalendarItem returns a calendar showing a day/month/year date.
func NewCalendarItem() *CalendarItem {
	return &CalendarItem{
		ItemBase:  newItemBase("Calendar"),
		TextStyle: defaultTextStyle(),
		Format:    "02/01/2006",
	}
}

// Kind implements DisplayItem.
func (c *CalendarItem) Kind() ItemKind { return KindCalendar }

// Clone implements DisplayItem.
func (c *CalendarItem) Clone() DisplayItem {
	cp := *c
	return &cp
}

// EvaluateTextAndColor implements TextEvaluator.
func (c *CalendarItem) EvaluateTextAndColor(ctx *EvalContext) (string, string) {
	return c.applyCase(ctx.now().Format(c.Format)), c.Color
}

// Bounds implements DisplayItem.
func (c *CalendarItem) Bounds(ctx *EvalContext) Rect {
	text, _ := c.EvaluateTextAndColor(ctx)
	return textBounds(&c.ItemBase, &c.TextStyle, text, ctx)
}
