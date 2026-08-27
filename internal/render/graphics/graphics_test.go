package graphics

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/render/font"
)

func newTestSurface(t *testing.T, w, h int) *Graphics {
	t.Helper()
	return New(w, h, Options{Fonts: font.NewCache(), FontScale: 1})
}

// pixelAt reads a pixel as non-premultiplied RGBA.
func pixelAt(g *Graphics, x, y int) color.NRGBA {
	r, gr, b, a := g.Image().At(x, y).RGBA()
	if a == 0 {
		return color.NRGBA{}
	}
	return color.NRGBA{
		R: uint8(r * 255 / a),
		G: uint8(gr * 255 / a),
		B: uint8(b * 255 / a),
		A: uint8(a >> 8),
	}
}

func TestClearReplacesRatherThanComposites(t *testing.T) {
	// An overlay window with per-pixel alpha needs a transparent clear to
	// genuinely erase the previous frame, not blend over it.
	g := newTestSurface(t, 10, 10)

	g.Clear(color.NRGBA{255, 0, 0, 255})
	g.Clear(color.NRGBA{0, 0, 0, 0})

	if got := pixelAt(g, 5, 5); got.A != 0 {
		t.Errorf("pixel after a transparent clear = %+v, want fully transparent", got)
	}
}

func TestFillRectPaintsInsideAndLeavesOutside(t *testing.T) {
	g := newTestSurface(t, 40, 40)
	g.Clear(color.NRGBA{0, 0, 0, 255})

	g.FillRect(Rect{X: 10, Y: 10, W: 20, H: 20}, 0,
		SolidFill(color.NRGBA{255, 0, 0, 255}), Transform{})

	if got := pixelAt(g, 20, 20); got.R != 255 {
		t.Errorf("center of the rect = %+v, want red", got)
	}
	if got := pixelAt(g, 2, 2); got.R != 0 {
		t.Errorf("outside the rect = %+v, want the cleared background", got)
	}
}

func TestFillRectIgnoresEmptyRectangles(t *testing.T) {
	g := newTestSurface(t, 20, 20)
	g.Clear(color.NRGBA{0, 0, 0, 255})

	for _, r := range []Rect{{X: 5, Y: 5, W: 0, H: 10}, {X: 5, Y: 5, W: 10, H: 0}} {
		g.FillRect(r, 0, SolidFill(color.NRGBA{255, 0, 0, 255}), Transform{})
	}

	if got := pixelAt(g, 5, 5); got.R != 0 {
		t.Errorf("an empty rectangle painted something: %+v", got)
	}
}

func TestDisabledFillPaintsNothing(t *testing.T) {
	g := newTestSurface(t, 20, 20)
	g.Clear(color.NRGBA{0, 0, 0, 255})

	g.FillRect(Rect{X: 2, Y: 2, W: 16, H: 16}, 0,
		Fill{Color: color.NRGBA{255, 0, 0, 255}}, Transform{})

	if got := pixelAt(g, 10, 10); got.R != 0 {
		t.Errorf("a fill with Enabled false painted: %+v", got)
	}
}

func TestGradientEndpointsSpanTheShape(t *testing.T) {
	box := Rect{X: 0, Y: 0, W: 100, H: 50}

	// A horizontal gradient runs the full width through the center.
	x0, y0, x1, y1 := gradientEndpoints(box, 0)
	if math.Abs(x0-0) > 0.01 || math.Abs(x1-100) > 0.01 {
		t.Errorf("horizontal endpoints x = %v..%v, want 0..100", x0, x1)
	}
	if math.Abs(y0-25) > 0.01 || math.Abs(y1-25) > 0.01 {
		t.Errorf("horizontal endpoints y = %v, %v, want both at the center 25", y0, y1)
	}

	// A vertical gradient runs the full height through the center.
	x0, y0, x1, y1 = gradientEndpoints(box, 90)
	if math.Abs(y0-0) > 0.01 || math.Abs(y1-50) > 0.01 {
		t.Errorf("vertical endpoints y = %v..%v, want 0..50", y0, y1)
	}
	if math.Abs(x0-50) > 0.01 || math.Abs(x1-50) > 0.01 {
		t.Errorf("vertical endpoints x = %v, %v, want both at the center 50", x0, x1)
	}
}

func TestGradientAxisAlwaysPassesThroughCenter(t *testing.T) {
	// Rotating the angle must spin the gradient in place rather than slide it
	// off the shape.
	box := Rect{X: 10, Y: 20, W: 80, H: 40}
	cx, cy := box.Center()

	for _, angle := range []float64{0, 30, 45, 90, 135, 180, 270} {
		x0, y0, x1, y1 := gradientEndpoints(box, angle)
		midX, midY := (x0+x1)/2, (y0+y1)/2
		if math.Abs(midX-cx) > 0.01 || math.Abs(midY-cy) > 0.01 {
			t.Errorf("at %v degrees the axis midpoint is (%v, %v), want the center (%v, %v)",
				angle, midX, midY, cx, cy)
		}
	}
}

func TestPathRectClampsCornerRadius(t *testing.T) {
	// A radius past half the shorter side would produce self-crossing corners.
	g := newTestSurface(t, 40, 40)
	g.Clear(color.NRGBA{0, 0, 0, 255})

	g.FillRect(Rect{X: 5, Y: 5, W: 30, H: 20}, 1000,
		SolidFill(color.NRGBA{0, 255, 0, 255}), Transform{})

	// The shape becomes a capsule; its center is still painted.
	if got := pixelAt(g, 20, 15); got.G != 255 {
		t.Errorf("center of an over-rounded rect = %+v, want green", got)
	}
}

func TestTransformOpacityDefaultsToOpaque(t *testing.T) {
	// A zero Opacity field must not make items invisible: it is the zero value
	// of a struct callers routinely leave unset.
	if got := (Transform{}).alpha(); got != 1 {
		t.Errorf("zero Transform alpha = %v, want 1", got)
	}
	if got := (Transform{Opacity: 0.5}).alpha(); got != 0.5 {
		t.Errorf("alpha = %v, want 0.5", got)
	}
	if got := (Transform{Opacity: 5}).alpha(); got != 1 {
		t.Errorf("out-of-range alpha = %v, want it clamped to 1", got)
	}
}

func TestRotationTurnsAboutTheShapeCenter(t *testing.T) {
	// A square rotated 90 degrees about its own center lands where it started.
	plain := newTestSurface(t, 60, 60)
	plain.Clear(color.NRGBA{0, 0, 0, 255})
	plain.FillRect(Rect{X: 20, Y: 20, W: 20, H: 20}, 0,
		SolidFill(color.NRGBA{255, 255, 255, 255}), Transform{})

	rotated := newTestSurface(t, 60, 60)
	rotated.Clear(color.NRGBA{0, 0, 0, 255})
	rotated.FillRect(Rect{X: 20, Y: 20, W: 20, H: 20}, 0,
		SolidFill(color.NRGBA{255, 255, 255, 255}), Transform{Rotation: 90})

	for _, p := range []image.Point{{X: 25, Y: 25}, {X: 35, Y: 35}, {X: 30, Y: 30}} {
		before := pixelAt(plain, p.X, p.Y)
		after := pixelAt(rotated, p.X, p.Y)
		if before.R != after.R {
			t.Errorf("at (%d,%d) rotation moved the square: %+v vs %+v", p.X, p.Y, before, after)
		}
	}
}

func TestDonutPaintsProgressAndBackground(t *testing.T) {
	g := newTestSurface(t, 100, 100)
	g.Clear(color.NRGBA{0, 0, 0, 255})

	g.Donut(Point{X: 50, Y: 50}, 40, 10, 0.5, 360,
		color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 0, 255, 255},
		Stroke{}, Transform{})

	// The ring band sits between radius-thickness and radius, so the hole and
	// the area outside must remain untouched.
	if got := pixelAt(g, 50, 50); got.R != 0 || got.B != 0 {
		t.Errorf("donut center = %+v, want the cleared background", got)
	}
	if got := pixelAt(g, 2, 2); got.R != 0 || got.B != 0 {
		t.Errorf("outside the donut = %+v, want the cleared background", got)
	}

	// Somewhere on the ring must be painted.
	painted := false
	for y := 0; y < 100 && !painted; y++ {
		for x := 0; x < 100; x++ {
			if p := pixelAt(g, x, y); p.R > 100 || p.B > 100 {
				painted = true
				break
			}
		}
	}
	if !painted {
		t.Error("the donut painted nothing")
	}
}

func TestMeasureTextGrowsWithContent(t *testing.T) {
	g := newTestSurface(t, 200, 100)

	short := g.MeasureText(TextSpec{Text: "Hi", Family: "Segoe UI", Size: 20})
	long := g.MeasureText(TextSpec{Text: "Hello there, world", Family: "Segoe UI", Size: 20})

	if short.Width <= 0 || short.Height <= 0 {
		t.Fatalf("short text measured %+v, want a positive extent", short)
	}
	if long.Width <= short.Width {
		t.Errorf("longer text measured %v wide, not more than %v", long.Width, short.Width)
	}
	if math.Abs(long.Height-short.Height) > 0.01 {
		t.Errorf("single-line heights differ: %v vs %v", long.Height, short.Height)
	}
}

func TestMeasureTextScalesWithFontSize(t *testing.T) {
	g := newTestSurface(t, 400, 200)

	small := g.MeasureText(TextSpec{Text: "Sample", Family: "Segoe UI", Size: 12})
	large := g.MeasureText(TextSpec{Text: "Sample", Family: "Segoe UI", Size: 36})

	if large.Width <= small.Width || large.Height <= small.Height {
		t.Errorf("36pt measured %+v, not larger than 12pt at %+v", large, small)
	}
}

func TestMeasureTextHonorsFontScale(t *testing.T) {
	unscaled := New(400, 200, Options{Fonts: font.NewCache(), FontScale: 1})
	scaled := New(400, 200, Options{Fonts: font.NewCache(), FontScale: 2})

	spec := TextSpec{Text: "Sample", Family: "Segoe UI", Size: 20}
	base := unscaled.MeasureText(spec)
	doubled := scaled.MeasureText(spec)

	if doubled.Width <= base.Width {
		t.Errorf("font scale 2 measured %v wide, not more than %v", doubled.Width, base.Width)
	}
}

func TestMeasureTextEmptyStringIsZero(t *testing.T) {
	g := newTestSurface(t, 100, 100)
	if got := g.MeasureText(TextSpec{Text: "", Family: "Segoe UI", Size: 20}); got != (Size{}) {
		t.Errorf("empty text measured %+v, want zero", got)
	}
}

func TestWrappedTextIsTallerThanOneLine(t *testing.T) {
	g := newTestSurface(t, 300, 200)

	spec := TextSpec{
		Text:   "This sentence is long enough that it must break across several lines.",
		Family: "Segoe UI", Size: 16, Wrap: true, Width: 120,
	}
	wrapped := g.MeasureText(spec)

	spec.Wrap = false
	spec.Width = 0
	single := g.MeasureText(spec)

	if wrapped.Height <= single.Height {
		t.Errorf("wrapped height %v is not greater than the single-line height %v",
			wrapped.Height, single.Height)
	}
	if wrapped.Width > 130 {
		t.Errorf("wrapped width %v exceeds the requested bound of 120", wrapped.Width)
	}
}

func TestHeightBoundCapsLineCount(t *testing.T) {
	g := newTestSurface(t, 300, 200)

	spec := TextSpec{
		Text:   "One two three four five six seven eight nine ten eleven twelve thirteen.",
		Family: "Segoe UI", Size: 16, Wrap: true, Ellipsis: true,
		Width: 100, Height: 40,
	}

	got := g.MeasureText(spec)
	if got.Height > 45 {
		t.Errorf("measured height %v, want it capped near the 40px bound", got.Height)
	}
}

func TestDrawTextRightAlignGrowsLeftward(t *testing.T) {
	// Sensor readouts rely on this: the right edge stays put as the value
	// widens, so a changing number does not jitter the layout.
	g := newTestSurface(t, 300, 100)
	g.Clear(color.NRGBA{0, 0, 0, 255})

	const anchor = 200.0
	g.DrawText(TextSpec{Text: "1234567890", Family: "Segoe UI", Size: 24}, anchor, 20,
		TextOptions{Color: color.NRGBA{255, 255, 255, 255}, Align: AlignRight})

	// Nothing should be painted to the right of the anchor.
	for y := 0; y < 100; y++ {
		for x := int(anchor) + 3; x < 300; x++ {
			if p := pixelAt(g, x, y); p.R > 40 {
				t.Fatalf("right-aligned text painted at x=%d, past the anchor at %v", x, anchor)
			}
		}
	}
}

func TestDrawTextReportsTheSizeItPainted(t *testing.T) {
	g := newTestSurface(t, 300, 100)

	spec := TextSpec{Text: "Measured", Family: "Segoe UI", Size: 20}
	measured := g.MeasureText(spec)
	drawn := g.DrawText(spec, 10, 10, TextOptions{Color: color.NRGBA{255, 255, 255, 255}})

	if measured != drawn {
		t.Errorf("DrawText reported %+v but MeasureText reported %+v; "+
			"selection rectangles would not match the glyphs", drawn, measured)
	}
}

func TestFitRectModes(t *testing.T) {
	box := Rect{X: 0, Y: 0, W: 100, H: 100}

	stretched := FitRect(box, 200, 100, FitStretch)
	if stretched != box {
		t.Errorf("FitStretch = %+v, want the box unchanged", stretched)
	}

	contained := FitRect(box, 200, 100, FitContain)
	if contained.W != 100 || contained.H != 50 {
		t.Errorf("FitContain = %+v, want 100x50 fitted inside", contained)
	}
	if contained.Y != 25 {
		t.Errorf("FitContain Y = %v, want it centered at 25", contained.Y)
	}

	covered := FitRect(box, 200, 100, FitCover)
	if covered.W != 200 || covered.H != 100 {
		t.Errorf("FitCover = %+v, want 200x100 covering the box", covered)
	}
}

func TestScaleProducesRequestedSize(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 20))
	out := Scale(src, 40, 80)

	if got := out.Bounds(); got.Dx() != 40 || got.Dy() != 80 {
		t.Errorf("Scale produced %v, want 40x80", got)
	}
}

func TestBoxBlurSpreadsAndPreservesEdges(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 21, 21))
	img.Set(10, 10, color.NRGBA{255, 255, 255, 255})

	boxBlur(img, 3)

	// The lone bright pixel must have spread to its neighbours.
	if _, _, _, a := img.At(12, 10).RGBA(); a == 0 {
		t.Error("blur did not spread into neighbouring pixels")
	}
	// The centre must have dimmed as its energy spread out.
	if _, _, _, a := img.At(10, 10).RGBA(); a>>8 == 255 {
		t.Error("blur left the centre pixel at full intensity")
	}
}

func TestBoxBlurDoesNotDarkenBorders(t *testing.T) {
	// The sliding window clamps at the edges rather than sampling
	// transparency, which would ring a dark border around every glow.
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			img.Set(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}

	boxBlur(img, 4)

	for _, p := range []image.Point{{X: 0, Y: 0}, {X: 19, Y: 0}, {X: 0, Y: 19}, {X: 19, Y: 19}} {
		_, _, _, a := img.At(p.X, p.Y).RGBA()
		if a>>8 < 250 {
			t.Errorf("corner (%d,%d) alpha = %d after blurring a uniform image, want it near 255",
				p.X, p.Y, a>>8)
		}
	}
}

func TestSnapshotIsIndependent(t *testing.T) {
	g := newTestSurface(t, 20, 20)
	g.Clear(color.NRGBA{255, 0, 0, 255})

	snap := g.Snapshot()
	g.Clear(color.NRGBA{0, 0, 255, 255})

	if r, _, _, _ := snap.At(10, 10).RGBA(); r>>8 != 255 {
		t.Error("later drawing changed a snapshot taken before it")
	}
}
