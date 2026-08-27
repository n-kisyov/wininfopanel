package draw

import (
	"image/color"
	"math"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
)

// drawTextLike paints any item that renders as a string: plain text, clocks,
// calendars, and sensor readouts. They differ only in how they produce their
// text and color, which the TextEvaluator interface supplies.
func drawTextLike(g *graphics.Graphics, base *model.ItemBase, style *model.TextStyle,
	item model.TextEvaluator, ctx *model.EvalContext, frame *Frame) {

	text, colorText := item.EvaluateTextAndColor(ctx)
	if text == "" {
		return
	}

	spec := textSpec(style, text)
	opts := graphics.TextOptions{
		Color:     graphics.ColorOr(colorText, color.NRGBA{0, 0, 0, 255}),
		Align:     alignOf(style),
		Glow:      glowOf(style),
		Transform: graphics.Transform{Rotation: float64(base.Rotation)},
	}

	if style.Marquee && style.Width > 0 {
		drawMarquee(g, base, style, spec, opts, frame)
		return
	}

	g.DrawText(spec, float64(base.X), float64(base.Y), opts)
}

// textSpec translates a display item's typography into a draw request.
func textSpec(style *model.TextStyle, text string) graphics.TextSpec {
	return graphics.TextSpec{
		Text:      text,
		Family:    style.Font,
		Size:      style.FontSize,
		Bold:      style.Bold,
		Italic:    style.Italic,
		Underline: style.Underline,
		Strikeout: style.Strikeout,
		Wrap:      style.Wrap,
		Ellipsis:  style.Ellipsis,
		Width:     style.Width,
		Height:    style.Height,
	}
}

func alignOf(style *model.TextStyle) graphics.TextAlign {
	switch {
	case style.CenterAlign:
		return graphics.AlignCenter
	case style.RightAlign:
		return graphics.AlignRight
	default:
		return graphics.AlignLeft
	}
}

func glowOf(style *model.TextStyle) graphics.Glow {
	if !style.Glow.Enabled {
		return graphics.Glow{}
	}
	return graphics.Glow{
		Enabled: true,
		Radius:  style.Glow.Radius,
		Color:   graphics.ColorOr(style.Glow.Color, color.NRGBA{0, 0, 0, 255}),
	}
}

// drawMarquee scrolls text horizontally within a fixed box.
//
// The text is drawn twice, one repetition apart, so the tail of the string
// follows its own head into view and the loop has no visible seam. Text that
// already fits its box is drawn stationary: scrolling something fully visible
// only makes it harder to read.
func drawMarquee(g *graphics.Graphics, base *model.ItemBase, style *model.TextStyle,
	spec graphics.TextSpec, opts graphics.TextOptions, frame *Frame) {

	// Marquee measures unwrapped and unbounded: the box scrolls past the
	// text, it does not reflow it.
	measureSpec := spec
	measureSpec.Wrap = false
	measureSpec.Width = 0
	measureSpec.Height = 0
	measureSpec.Ellipsis = false

	size := g.MeasureText(measureSpec)
	box := graphics.Rect{
		X: float64(base.X),
		Y: float64(base.Y),
		W: float64(style.Width),
		H: size.Height,
	}
	if style.Height > 0 {
		box.H = float64(style.Height)
	}

	if size.Width <= box.W {
		g.DrawText(measureSpec, box.X, box.Y, opts)
		return
	}

	spacing := float64(style.MarqueeSpacing)
	if spacing < 0 {
		spacing = 0
	}
	period := size.Width + spacing

	speed := float64(style.MarqueeSpeed)
	offset := 0.0
	if speed > 0 && period > 0 {
		// Position derives from the frame clock rather than an accumulated
		// counter, so the animation is identical for every consumer of the
		// same frame and does not drift when a render is skipped.
		elapsed := float64(frame.now().UnixNano()) / float64(1e9)
		offset = math.Mod(elapsed*speed, period)
	}

	// Marquee ignores alignment: the text starts at the box's left edge and
	// travels leftward.
	scrollOpts := opts
	scrollOpts.Align = graphics.AlignLeft

	g.Clip(box, func() {
		g.DrawText(measureSpec, box.X-offset, box.Y, scrollOpts)
		g.DrawText(measureSpec, box.X-offset+period, box.Y, scrollOpts)
	})
}

// drawTable paints a plugin-provided table.
//
// Column widths come from the item's format string; rows come from the bound
// sensor's table payload. Until the plugin host lands there is no table data
// to read, so the item renders its header and an empty body -- visible in the
// layout, honest about having nothing to show.
func drawTable(g *graphics.Graphics, item *model.TableItem, ctx *model.EvalContext, frame *Frame) {
	columns := model.ParseTableFormat(item.Format)
	if len(columns) == 0 {
		return
	}

	style := &item.TextStyle
	textColor := graphics.ColorOr(style.Color, color.NRGBA{0, 0, 0, 255})
	lineHeight := g.MeasureText(textSpec(style, "X")).Height
	if lineHeight <= 0 {
		return
	}

	rows := tableRows(item, ctx)

	transform := graphics.Transform{Rotation: float64(item.Rotation)}
	y := float64(item.Y)

	if item.ShowHeader {
		x := float64(item.X)
		for _, column := range columns {
			header := columnHeader(rows, column.Index)
			spec := textSpec(style, header)
			spec.Bold = true
			spec.Width = column.Width
			spec.Wrap = false

			g.DrawText(spec, x, y, graphics.TextOptions{
				Color: textColor, Transform: transform,
			})
			x += float64(column.Width)
		}
		y += lineHeight
	}

	limit := item.MaxRows
	if limit <= 0 || limit > len(rows) {
		limit = len(rows)
	}

	for _, row := range rows[:limit] {
		x := float64(item.X)
		for _, column := range columns {
			spec := textSpec(style, row.cell(column.Index))
			spec.Width = column.Width
			spec.Wrap = false

			g.DrawText(spec, x, y, graphics.TextOptions{
				Color: textColor, Transform: transform,
			})
			x += float64(column.Width)
		}
		y += lineHeight
	}
}

// tableRow is one row of a plugin table.
type tableRow struct {
	headers []string
	cells   []string
}

func (r tableRow) cell(index int) string {
	if index < 0 || index >= len(r.cells) {
		return ""
	}
	return r.cells[index]
}

// tableRows resolves a table item's rows.
//
// Table payloads arrive over the plugin IPC channel, which does not exist yet;
// this returns nothing until it does.
func tableRows(*model.TableItem, *model.EvalContext) []tableRow {
	return nil
}

// columnHeader returns a column's label, or a positional placeholder when the
// data carries no header.
func columnHeader(rows []tableRow, index int) string {
	for _, row := range rows {
		if index >= 0 && index < len(row.headers) {
			return row.headers[index]
		}
	}
	return ""
}
