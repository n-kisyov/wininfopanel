// Package graphics is wininfopanel's drawing surface, the equivalent of
// InfoPanel's SkiaGraphics.
//
// It renders in pixel coordinates with the origin at the top left, directly
// into an image.RGBA -- the format both the layered overlay windows and the
// USB panel encoders consume, so a rendered frame reaches either destination
// without a conversion step.
//
// Everything here is software rasterized and cgo-free. The surface is an
// interface boundary on purpose: if a GPU-backed backend is ever warranted,
// the drawing code above it does not change.
package graphics

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/fogleman/gg"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
)

// Graphics is a drawing surface backed by an RGBA image.
//
// It is not safe for concurrent use: each render target owns one.
type Graphics struct {
	ctx *gg.Context
	img *image.RGBA

	fonts *font.Cache
	// fontScale converts a profile's design-unit font sizes into pixels.
	fontScale float64
}

// Options configures a surface.
type Options struct {
	// Fonts resolves family names to faces. Required for text drawing.
	Fonts *font.Cache
	// FontScale multiplies every font size, compensating for the difference
	// between a profile's design units and rendered pixels. Defaults to 1.
	FontScale float64
}

// New returns a surface of the given pixel size, initially fully transparent.
func New(width, height int, opts Options) *Graphics {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return FromImage(image.NewRGBA(image.Rect(0, 0, width, height)), opts)
}

// FromImage returns a surface drawing into an existing image.
//
// The image is not cleared, so a caller can composite onto an existing frame.
func FromImage(img *image.RGBA, opts Options) *Graphics {
	if opts.FontScale <= 0 {
		opts.FontScale = 1
	}
	return &Graphics{
		ctx:       gg.NewContextForRGBA(img),
		img:       img,
		fonts:     opts.Fonts,
		fontScale: opts.FontScale,
	}
}

// Image returns the surface's backing image.
//
// The image aliases the surface: further drawing is visible through it.
func (g *Graphics) Image() *image.RGBA { return g.img }

// Width returns the surface width in pixels.
func (g *Graphics) Width() int { return g.img.Bounds().Dx() }

// Height returns the surface height in pixels.
func (g *Graphics) Height() int { return g.img.Bounds().Dy() }

// FontScale returns the factor applied to font sizes.
func (g *Graphics) FontScale() float64 { return g.fontScale }

// Fonts returns the surface's font cache, which may be nil.
func (g *Graphics) Fonts() *font.Cache { return g.fonts }

// Clear fills the whole surface with a single color, replacing what is there
// rather than compositing over it.
//
// A transparent clear therefore genuinely erases, which is what an overlay
// window with per-pixel alpha needs between frames.
func (g *Graphics) Clear(c color.NRGBA) {
	draw.Draw(g.img, g.img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
}

// Snapshot returns an independent copy of the current frame, for callers that
// need the pixels to stay stable while drawing continues.
func (g *Graphics) Snapshot() *image.RGBA {
	out := image.NewRGBA(g.img.Bounds())
	copy(out.Pix, g.img.Pix)
	return out
}

// withRotation runs body with the surface rotated by degrees about (cx, cy),
// restoring the previous transform afterwards.
//
// A zero rotation skips the transform entirely: it is the common case, and
// pushing a matrix for it would cost a resample on every item.
func (g *Graphics) withRotation(degrees float64, cx, cy float64, body func()) {
	if degrees == 0 {
		body()
		return
	}

	g.ctx.Push()
	g.ctx.RotateAbout(gg.Radians(degrees), cx, cy)
	body()
	g.ctx.Pop()
}

// Transform describes the placement adjustments an item can request.
type Transform struct {
	// Rotation is in degrees, clockwise, about the item's center.
	Rotation float64
	// FlipX mirrors horizontally about the item's center.
	FlipX bool
	// Opacity scales alpha, 0..1. Zero is treated as fully opaque so that an
	// unset field does not make items invisible.
	Opacity float64
}

// alpha returns the effective opacity, resolving the zero value.
func (t Transform) alpha() float64 {
	if t.Opacity <= 0 || t.Opacity > 1 {
		return 1
	}
	return t.Opacity
}

// Rect is an axis-aligned rectangle in surface pixels.
type Rect struct {
	X, Y, W, H float64
}

// Center returns the rectangle's midpoint, the origin rotations turn about.
func (r Rect) Center() (float64, float64) {
	return r.X + r.W/2, r.Y + r.H/2
}

// IsEmpty reports whether the rectangle encloses no area.
func (r Rect) IsEmpty() bool { return r.W <= 0 || r.H <= 0 }

// clampByte constrains a value to a valid color channel.
func clampByte(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return uint8(v + 0.5)
	}
}

// clamp01 constrains a value to the unit interval.
func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}
