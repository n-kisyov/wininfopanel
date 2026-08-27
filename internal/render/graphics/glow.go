package graphics

import (
	"image"
	"image/draw"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
)

// Text glow is drawn by rendering the run into a scratch layer, blurring that
// layer, and compositing it beneath the text. gg has no blur, so one is
// implemented here.

// drawGlow paints a blurred halo behind a text run.
//
// The scratch layer is sized to the text plus the blur radius on every side,
// since the blur spreads beyond the glyphs.
func (g *Graphics) drawGlow(lines []string, widths []float64, face font.Face,
	box Rect, lineHeight float64, opts TextOptions) {

	radius := opts.Glow.Radius
	if radius <= 0 {
		return
	}

	pad := radius * 2
	width := int(box.W) + pad*2
	height := int(box.H) + pad*2
	if width <= 0 || height <= 0 {
		return
	}

	layer := image.NewRGBA(image.Rect(0, 0, width, height))
	layerCtx := gg.NewContextForRGBA(layer)
	layerCtx.SetFontFace(face)
	layerCtx.SetColor(WithAlpha(opts.Glow.Color, opts.alpha()))

	metrics := face.Metrics()
	ascent := float64(metrics.Ascent) / 64

	// Draw into the layer in its own coordinates, offset by the padding.
	inner := Rect{X: float64(pad), Y: float64(pad), W: box.W, H: box.H}
	for i, line := range lines {
		lineY := inner.Y + float64(i)*lineHeight
		lineX := alignLine(inner, widths[i], opts.Align)
		layerCtx.DrawString(line, lineX, lineY+ascent)
	}

	boxBlur(layer, radius)

	// Composite the halo beneath where the text will be drawn.
	target := image.Rect(
		int(box.X)-pad, int(box.Y)-pad,
		int(box.X)-pad+width, int(box.Y)-pad+height,
	)
	draw.Draw(g.img, target, layer, image.Point{}, draw.Over)
}

// boxBlur approximates a Gaussian blur in place with three box passes.
//
// Three successive box blurs converge closely on a Gaussian while staying
// linear in the radius, which matters because this runs per frame for any
// glowing text on a panel.
func boxBlur(img *image.RGBA, radius int) {
	if radius <= 0 {
		return
	}

	scratch := image.NewRGBA(img.Bounds())
	for i := 0; i < 3; i++ {
		blurHorizontal(img, scratch, radius)
		blurVertical(scratch, img, radius)
	}
}

// blurHorizontal averages each row over a sliding window.
//
// The running sum is updated by adding the entering pixel and subtracting the
// leaving one, so cost is independent of the radius.
func blurHorizontal(src, dst *image.RGBA, radius int) {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return
	}

	window := radius*2 + 1

	for y := 0; y < height; y++ {
		rowStart := y * src.Stride

		var sumR, sumG, sumB, sumA int
		// Seed the window, clamping at the row edges so borders do not darken.
		for i := -radius; i <= radius; i++ {
			x := clampIndex(i, width)
			o := rowStart + x*4
			sumR += int(src.Pix[o])
			sumG += int(src.Pix[o+1])
			sumB += int(src.Pix[o+2])
			sumA += int(src.Pix[o+3])
		}

		for x := 0; x < width; x++ {
			o := y*dst.Stride + x*4
			dst.Pix[o] = uint8(sumR / window)
			dst.Pix[o+1] = uint8(sumG / window)
			dst.Pix[o+2] = uint8(sumB / window)
			dst.Pix[o+3] = uint8(sumA / window)

			leaving := rowStart + clampIndex(x-radius, width)*4
			entering := rowStart + clampIndex(x+radius+1, width)*4
			sumR += int(src.Pix[entering]) - int(src.Pix[leaving])
			sumG += int(src.Pix[entering+1]) - int(src.Pix[leaving+1])
			sumB += int(src.Pix[entering+2]) - int(src.Pix[leaving+2])
			sumA += int(src.Pix[entering+3]) - int(src.Pix[leaving+3])
		}
	}
}

// blurVertical averages each column over a sliding window.
func blurVertical(src, dst *image.RGBA, radius int) {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return
	}

	window := radius*2 + 1

	for x := 0; x < width; x++ {
		column := x * 4

		var sumR, sumG, sumB, sumA int
		for i := -radius; i <= radius; i++ {
			o := clampIndex(i, height)*src.Stride + column
			sumR += int(src.Pix[o])
			sumG += int(src.Pix[o+1])
			sumB += int(src.Pix[o+2])
			sumA += int(src.Pix[o+3])
		}

		for y := 0; y < height; y++ {
			o := y*dst.Stride + column
			dst.Pix[o] = uint8(sumR / window)
			dst.Pix[o+1] = uint8(sumG / window)
			dst.Pix[o+2] = uint8(sumB / window)
			dst.Pix[o+3] = uint8(sumA / window)

			leaving := clampIndex(y-radius, height)*src.Stride + column
			entering := clampIndex(y+radius+1, height)*src.Stride + column
			sumR += int(src.Pix[entering]) - int(src.Pix[leaving])
			sumG += int(src.Pix[entering+1]) - int(src.Pix[leaving+1])
			sumB += int(src.Pix[entering+2]) - int(src.Pix[leaving+2])
			sumA += int(src.Pix[entering+3]) - int(src.Pix[leaving+3])
		}
	}
}

// clampIndex holds a sampling index inside the image, so the window extends
// the edge pixel rather than sampling transparency and darkening borders.
func clampIndex(i, limit int) int {
	if i < 0 {
		return 0
	}
	if i >= limit {
		return limit - 1
	}
	return i
}
