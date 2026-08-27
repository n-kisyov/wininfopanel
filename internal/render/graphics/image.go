package graphics

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"
)

// ImageOptions controls how a bitmap is placed on the surface.
type ImageOptions struct {
	Transform

	// Tint, when its alpha is non-zero, blends a flat color over the image.
	// It is how a single icon set is recolored per profile.
	Tint color.NRGBA
}

// DrawImage paints src into the given rectangle, scaling to fit.
//
// An empty destination rectangle draws the image at its natural size, which is
// what an unsized image display item asks for.
func (g *Graphics) DrawImage(src image.Image, dst Rect, opts ImageOptions) {
	if src == nil {
		return
	}

	bounds := src.Bounds()
	if bounds.Empty() {
		return
	}

	if dst.W <= 0 {
		dst.W = float64(bounds.Dx())
	}
	if dst.H <= 0 {
		dst.H = float64(bounds.Dy())
	}
	if dst.IsEmpty() {
		return
	}

	prepared := src
	if opts.Tint.A > 0 {
		prepared = tintImage(prepared, opts.Tint)
	}

	cx, cy := dst.Center()
	g.withRotation(opts.Rotation, cx, cy, func() {
		g.ctx.Push()
		defer g.ctx.Pop()

		// Mirroring is a negative horizontal scale about the destination's
		// center, which keeps the image in place while reversing it.
		if opts.FlipX {
			g.ctx.Translate(cx, 0)
			g.ctx.Scale(-1, 1)
			g.ctx.Translate(-cx, 0)
		}

		scaleX := dst.W / float64(bounds.Dx())
		scaleY := dst.H / float64(bounds.Dy())

		g.ctx.Translate(dst.X, dst.Y)
		g.ctx.Scale(scaleX, scaleY)

		if alpha := opts.alpha(); alpha < 1 {
			g.ctx.DrawImageAnchored(fadeImage(prepared, alpha), 0, 0, 0, 0)
			return
		}
		g.ctx.DrawImageAnchored(prepared, 0, 0, 0, 0)
	})
}

// Scale returns src resized to the given pixel dimensions.
//
// CatmullRom is used for its sharpness on the text and fine detail that panel
// artwork is full of; a box filter would visibly soften it.
func Scale(src image.Image, width, height int) *image.RGBA {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	out := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(out, out.Bounds(), src, src.Bounds(), draw.Over, nil)
	return out
}

// tintImage blends a flat color over an image, preserving the original alpha
// so the tint follows the artwork's silhouette.
func tintImage(src image.Image, tint color.NRGBA) *image.RGBA {
	bounds := src.Bounds()
	out := image.NewRGBA(bounds)

	strength := float64(tint.A) / 255

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, gr, b, a := src.At(x, y).RGBA()
			if a == 0 {
				continue
			}

			// Un-premultiply so the blend operates on the visible color, then
			// re-premultiply for storage.
			alpha := float64(a) / 65535
			sr := float64(r) / 65535 / alpha * 255
			sg := float64(gr) / 65535 / alpha * 255
			sb := float64(b) / 65535 / alpha * 255

			blended := color.NRGBA{
				R: clampByte(sr + (float64(tint.R)-sr)*strength),
				G: clampByte(sg + (float64(tint.G)-sg)*strength),
				B: clampByte(sb + (float64(tint.B)-sb)*strength),
				A: clampByte(alpha * 255),
			}
			out.Set(x, y, blended)
		}
	}
	return out
}

// fadeImage returns src with its alpha scaled uniformly.
func fadeImage(src image.Image, opacity float64) *image.RGBA {
	opacity = clamp01(opacity)
	bounds := src.Bounds()
	out := image.NewRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := src.At(x, y).RGBA()
			out.SetRGBA64(x, y, color.RGBA64{
				R: uint16(float64(r) * opacity),
				G: uint16(float64(g) * opacity),
				B: uint16(float64(b) * opacity),
				A: uint16(float64(a) * opacity),
			})
		}
	}
	return out
}

// FitMode selects how an image is sized into a box.
type FitMode int

const (
	// FitStretch fills the box exactly, distorting the aspect ratio.
	FitStretch FitMode = iota
	// FitContain scales until the image fits entirely inside the box.
	FitContain
	// FitCover scales until the box is entirely covered, cropping overflow.
	FitCover
)

// FitRect computes the destination rectangle for an image of the given natural
// size placed into a box under the chosen fit mode.
func FitRect(box Rect, naturalWidth, naturalHeight int, mode FitMode) Rect {
	if naturalWidth <= 0 || naturalHeight <= 0 || box.IsEmpty() {
		return box
	}
	if mode == FitStretch {
		return box
	}

	scaleX := box.W / float64(naturalWidth)
	scaleY := box.H / float64(naturalHeight)

	scale := math.Min(scaleX, scaleY)
	if mode == FitCover {
		scale = math.Max(scaleX, scaleY)
	}

	w := float64(naturalWidth) * scale
	h := float64(naturalHeight) * scale
	return Rect{
		X: box.X + (box.W-w)/2,
		Y: box.Y + (box.H-h)/2,
		W: w,
		H: h,
	}
}

// Clip restricts drawing to a rectangle for the duration of body.
//
// Used for marquee text and any item whose content must not spill past its
// declared bounds.
func (g *Graphics) Clip(r Rect, body func()) {
	if r.IsEmpty() {
		return
	}

	g.ctx.Push()
	g.ctx.DrawRectangle(r.X, r.Y, r.W, r.H)
	g.ctx.Clip()
	body()
	g.ctx.ResetClip()
	g.ctx.Pop()
}
