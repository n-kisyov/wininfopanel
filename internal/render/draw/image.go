package draw

import (
	"image"
	"image/color"
	"math"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
)

// ImageSource resolves an image display item to the bitmap it should draw.
//
// Decoding, caching, GIF frame timing, HTTP fetching, and video playback all
// live behind this interface, which keeps this package concerned only with
// placement.
type ImageSource interface {
	// Resolve returns the bitmap for an item at the current moment. The
	// profile ID resolves items whose path is relative to their profile's
	// asset directory.
	//
	// The second result is false when the image is unavailable -- still
	// downloading, not found, or failed to decode -- in which case nothing is
	// drawn.
	Resolve(profileID string, item *model.ImageItem) (image.Image, bool)
}

// drawImage paints an image display item.
func drawImage(g *graphics.Graphics, item *model.ImageItem, _ *model.EvalContext, frame *Frame) {
	if frame.Images == nil {
		return
	}

	src, ok := frame.Images.Resolve(frame.profileID(), item)
	if !ok || src == nil {
		return
	}

	bounds := src.Bounds()
	dst := imageRect(item, bounds.Dx(), bounds.Dy())
	if dst.IsEmpty() {
		return
	}

	opts := graphics.ImageOptions{
		Transform: graphics.Transform{
			Rotation: float64(item.Rotation),
			FlipX:    item.FlipX,
			Opacity:  item.EffectiveOpacity(),
		},
	}
	if item.Layer {
		opts.Tint = graphics.ColorOr(item.LayerColor, color.NRGBA{})
	}

	g.DrawImage(src, dst, opts)
}

// imageRect computes where an image lands.
//
// Explicit dimensions win; otherwise the natural size is scaled by the item's
// percentage, which is how InfoPanel sizes artwork that has no declared box.
func imageRect(item *model.ImageItem, naturalWidth, naturalHeight int) graphics.Rect {
	width := float64(item.Width)
	height := float64(item.Height)

	if width <= 0 || height <= 0 {
		scale := float64(item.Scale) / 100
		if scale <= 0 {
			scale = 1
		}
		if width <= 0 {
			width = float64(naturalWidth) * scale
		}
		if height <= 0 {
			height = float64(naturalHeight) * scale
		}
	}

	return graphics.Rect{X: float64(item.X), Y: float64(item.Y), W: width, H: height}
}

// drawGauge paints the frame of an image set selected by a sensor value.
//
// The value picks a position in the set; the whole part chooses a frame and
// the fraction cross-fades into the next one, so a gauge sweeps smoothly
// rather than stepping between images.
func drawGauge(g *graphics.Graphics, item *model.GaugeItem, ctx *model.EvalContext, frame *Frame) {
	if len(item.Images) == 0 || frame.Images == nil {
		return
	}

	position, ok := item.FrameIndex(ctx)
	if !ok {
		return
	}

	// Easing is applied to the frame position rather than the sensor value, so
	// the animation speed is in frames per second as the property describes.
	if item.AnimationSpeed > 0 {
		position = frame.smooth(item.ID, position)
	}

	lower := int(math.Floor(position))
	upper := lower + 1
	blend := position - float64(lower)

	lower = clampIndex(lower, len(item.Images))
	upper = clampIndex(upper, len(item.Images))

	base := drawGaugeFrame(g, item, item.Images[lower], frame, 1)
	if upper != lower && blend > 0 && base {
		drawGaugeFrame(g, item, item.Images[upper], frame, blend)
	}
}

// drawGaugeFrame paints one frame of a gauge at the gauge's own position and
// size, reporting whether anything was drawn.
//
// The frame's own position is ignored: gauge images are a stack sharing the
// gauge's box, not independently placed items.
func drawGaugeFrame(g *graphics.Graphics, gauge *model.GaugeItem, img *model.ImageItem,
	frame *Frame, opacity float64) bool {

	src, ok := frame.Images.Resolve(frame.profileID(), img)
	if !ok || src == nil {
		return false
	}

	bounds := src.Bounds()
	placed := &model.ImageItem{
		ItemBase: model.ItemBase{X: gauge.X, Y: gauge.Y},
		Width:    gauge.Width,
		Height:   gauge.Height,
		Scale:    gauge.Scale,
	}
	dst := imageRect(placed, bounds.Dx(), bounds.Dy())
	if dst.IsEmpty() {
		return false
	}

	g.DrawImage(src, dst, graphics.ImageOptions{
		Transform: graphics.Transform{
			Rotation: float64(gauge.Rotation),
			FlipX:    gauge.FlipX,
			Opacity:  opacity,
		},
	})
	return true
}

// clampIndex holds an index inside a slice.
func clampIndex(i, length int) int {
	if i < 0 {
		return 0
	}
	if i >= length {
		return length - 1
	}
	return i
}
