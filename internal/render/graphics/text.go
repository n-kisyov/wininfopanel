package graphics

import (
	"image/color"
	"strings"

	"golang.org/x/image/font"

	fontcache "github.com/n-kisyov/wininfopanel/internal/render/font"
)

// TextSpec describes a run of text completely enough to measure or draw it.
type TextSpec struct {
	Text      string
	Family    string
	Size      int
	Bold      bool
	Italic    bool
	Underline bool
	Strikeout bool

	// Wrap breaks text at word boundaries to fit Width.
	Wrap bool
	// Ellipsis truncates with a trailing "..." when text exceeds the box.
	Ellipsis bool

	// Width bounds the text box in pixels; 0 means size to content.
	Width int
	// Height bounds the text box in pixels; 0 means size to content.
	Height int
}

// TextAlign selects horizontal placement relative to the anchor point.
type TextAlign int

const (
	// AlignLeft places the anchor at the text's left edge.
	AlignLeft TextAlign = iota
	// AlignCenter places the anchor at the text's midpoint.
	AlignCenter
	// AlignRight places the anchor at the text's right edge.
	AlignRight
)

// Glow describes the soft halo drawn behind text.
type Glow struct {
	Enabled bool
	Radius  int
	Color   color.NRGBA
}

// TextOptions controls how a run is placed and decorated.
type TextOptions struct {
	Color color.NRGBA
	Align TextAlign
	Glow  Glow
	Transform
}

// Size is a measured extent in pixels.
type Size struct {
	Width  float64
	Height float64
}

// face resolves a spec to a rendering face, applying the surface's font scale.
func (g *Graphics) face(spec TextSpec) (font.Face, error) {
	size := float64(spec.Size) * g.fontScale
	if size <= 0 {
		size = 1
	}
	return g.fonts.Face(spec.Family, fontcache.Style{Bold: spec.Bold, Italic: spec.Italic}, size)
}

// MeasureText returns the rendered extent of a run.
//
// Measurement and drawing share the same layout routine, so a selection
// rectangle on the design canvas lines up with the glyphs actually painted.
func (g *Graphics) MeasureText(spec TextSpec) Size {
	if g.fonts == nil || spec.Text == "" {
		return Size{}
	}

	face, err := g.face(spec)
	if err != nil {
		return Size{}
	}

	lines, lineHeight := layoutText(g.ctx, face, spec)
	if len(lines) == 0 {
		return Size{}
	}

	g.ctx.SetFontFace(face)
	var widest float64
	for _, line := range lines {
		if w, _ := g.ctx.MeasureString(line); w > widest {
			widest = w
		}
	}

	return Size{Width: widest, Height: lineHeight * float64(len(lines))}
}

// DrawText paints a run with its top-left corner at (x, y).
//
// The anchor is the top-left rather than the baseline because that is how
// display items store their position, and converting once here keeps every
// call site free of font-metric arithmetic.
func (g *Graphics) DrawText(spec TextSpec, x, y float64, opts TextOptions) Size {
	if g.fonts == nil || spec.Text == "" {
		return Size{}
	}

	face, err := g.face(spec)
	if err != nil {
		return Size{}
	}

	lines, lineHeight := layoutText(g.ctx, face, spec)
	if len(lines) == 0 {
		return Size{}
	}

	g.ctx.SetFontFace(face)

	var widest float64
	widths := make([]float64, len(lines))
	for i, line := range lines {
		w, _ := g.ctx.MeasureString(line)
		widths[i] = w
		if w > widest {
			widest = w
		}
	}
	size := Size{Width: widest, Height: lineHeight * float64(len(lines))}

	// The anchor moves rather than the text when aligning, matching how
	// InfoPanel positions right-aligned readouts: the right edge stays put and
	// the text grows leftward as the value widens.
	originX := x
	switch opts.Align {
	case AlignCenter:
		originX = x - size.Width/2
	case AlignRight:
		originX = x - size.Width
	}

	box := Rect{X: originX, Y: y, W: size.Width, H: size.Height}
	cx, cy := box.Center()
	opacity := opts.alpha()

	g.withRotation(opts.Rotation, cx, cy, func() {
		if opts.Glow.Enabled && opts.Glow.Radius > 0 {
			g.drawGlow(lines, widths, face, box, lineHeight, opts)
		}

		g.ctx.SetColor(WithAlpha(opts.Color, opacity))
		metrics := face.Metrics()
		ascent := float64(metrics.Ascent) / 64

		for i, line := range lines {
			lineY := box.Y + float64(i)*lineHeight
			lineX := alignLine(box, widths[i], opts.Align)

			g.ctx.DrawString(line, lineX, lineY+ascent)
			g.drawDecorations(spec, lineX, lineY, widths[i], lineHeight, ascent)
		}
	})

	return size
}

// alignLine positions one line within the text box.
func alignLine(box Rect, lineWidth float64, align TextAlign) float64 {
	switch align {
	case AlignCenter:
		return box.X + (box.W-lineWidth)/2
	case AlignRight:
		return box.X + box.W - lineWidth
	default:
		return box.X
	}
}

// drawDecorations paints underline and strikeout rules, which the font itself
// does not provide.
func (g *Graphics) drawDecorations(spec TextSpec, x, y, width, lineHeight, ascent float64) {
	if !spec.Underline && !spec.Strikeout {
		return
	}

	// Rule weight tracks the type size so decorations stay proportional.
	thickness := lineHeight / 16
	if thickness < 1 {
		thickness = 1
	}
	g.ctx.SetLineWidth(thickness)

	if spec.Underline {
		// Just below the baseline, clear of most descenders.
		underlineY := y + ascent + thickness*2
		g.ctx.DrawLine(x, underlineY, x+width, underlineY)
		g.ctx.Stroke()
	}
	if spec.Strikeout {
		strikeY := y + ascent*0.65
		g.ctx.DrawLine(x, strikeY, x+width, strikeY)
		g.ctx.Stroke()
	}
}

// layoutText splits a run into the lines that will actually be drawn, applying
// wrapping, truncation, and the height bound.
//
// The returned line height is the font's full line spacing.
func layoutText(ctx interface {
	MeasureString(string) (float64, float64)
	SetFontFace(font.Face)
	WordWrap(string, float64) []string
}, face font.Face, spec TextSpec) ([]string, float64) {

	ctx.SetFontFace(face)

	metrics := face.Metrics()
	lineHeight := float64(metrics.Height) / 64
	if lineHeight <= 0 {
		// A font with no reported line spacing still has to advance, or every
		// line would draw on top of the last.
		lineHeight = float64(spec.Size)
	}

	// Explicit newlines always break, independently of wrapping.
	var lines []string
	for _, paragraph := range strings.Split(spec.Text, "\n") {
		if spec.Wrap && spec.Width > 0 {
			wrapped := ctx.WordWrap(paragraph, float64(spec.Width))
			if len(wrapped) == 0 {
				// WordWrap drops empty input; keep the blank line so
				// paragraph spacing survives.
				wrapped = []string{""}
			}
			lines = append(lines, wrapped...)
			continue
		}
		lines = append(lines, paragraph)
	}

	// Without wrapping, a width bound truncates instead.
	if !spec.Wrap && spec.Width > 0 {
		for i, line := range lines {
			lines[i] = truncateToWidth(ctx, line, float64(spec.Width), spec.Ellipsis)
		}
	}

	// A height bound caps the line count, with the last visible line
	// truncated so the text visibly continues rather than stopping mid-thought.
	if spec.Height > 0 && lineHeight > 0 {
		maxLines := int(float64(spec.Height) / lineHeight)
		if maxLines < 1 {
			maxLines = 1
		}
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			if spec.Ellipsis && len(lines) > 0 {
				last := len(lines) - 1
				width := float64(spec.Width)
				if width <= 0 {
					width, _ = ctx.MeasureString(lines[last])
				}
				lines[last] = truncateToWidth(ctx, lines[last], width, true)
			}
		}
	}

	return lines, lineHeight
}

// truncateToWidth shortens text to fit, optionally marking the cut with an
// ellipsis.
func truncateToWidth(ctx interface {
	MeasureString(string) (float64, float64)
}, text string, maxWidth float64, ellipsis bool) string {

	if maxWidth <= 0 || text == "" {
		return text
	}
	if w, _ := ctx.MeasureString(text); w <= maxWidth {
		return text
	}

	const suffix = "..."
	runes := []rune(text)

	if !ellipsis {
		for n := len(runes) - 1; n > 0; n-- {
			if w, _ := ctx.MeasureString(string(runes[:n])); w <= maxWidth {
				return string(runes[:n])
			}
		}
		return ""
	}

	// Binary search for the longest prefix that fits alongside the suffix.
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if w, _ := ctx.MeasureString(string(runes[:mid]) + suffix); w <= maxWidth {
			low = mid
		} else {
			high = mid - 1
		}
	}
	if low == 0 {
		// Not even the ellipsis fits; an empty result is less misleading than
		// text overflowing its box.
		if w, _ := ctx.MeasureString(suffix); w <= maxWidth {
			return suffix
		}
		return ""
	}
	return string(runes[:low]) + suffix
}
