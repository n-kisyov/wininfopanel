package draw

import (
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

func newSurface(w, h int) *graphics.Graphics {
	return graphics.New(w, h, graphics.Options{Fonts: font.NewCache(), FontScale: 1})
}

// fixedSensors resolves every key to one reading.
type fixedSensors struct{ reading sensor.Reading }

func (f fixedSensors) Read(sensor.Key) (sensor.Reading, bool) { return f.reading, true }

// countPainted counts pixels differing from the background.
func countPainted(g *graphics.Graphics, background color.NRGBA) int {
	count := 0
	img := g.Image()
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, gr, b, a := img.At(x, y).RGBA()
			if uint8(r>>8) != background.R || uint8(gr>>8) != background.G ||
				uint8(b>>8) != background.B || uint8(a>>8) != background.A {
				count++
			}
		}
	}
	return count
}

func TestHiddenItemsAreNotDrawn(t *testing.T) {
	profile := model.NewProfile("Test", 100, 100)
	profile.BackgroundColor = "#FF000000"

	shape := model.NewShapeItem("Box", model.ShapeRectangle)
	shape.X, shape.Y, shape.Width, shape.Height = 10, 10, 50, 50
	shape.FillColor = "#FFFF0000"

	visible := newSurface(100, 100)
	Render(visible, model.ItemList{shape}, Frame{Profile: profile})
	visibleCount := countPainted(visible, color.NRGBA{0, 0, 0, 255})
	if visibleCount == 0 {
		t.Fatal("a visible shape painted nothing")
	}

	shape.Hidden = true
	hidden := newSurface(100, 100)
	Render(hidden, model.ItemList{shape}, Frame{Profile: profile})
	if got := countPainted(hidden, color.NRGBA{0, 0, 0, 255}); got != 0 {
		t.Errorf("a hidden item painted %d pixels, want 0", got)
	}
}

func TestGroupChildrenAreDrawn(t *testing.T) {
	profile := model.NewProfile("Test", 100, 100)
	profile.BackgroundColor = "#FF000000"

	child := model.NewShapeItem("Box", model.ShapeRectangle)
	child.X, child.Y, child.Width, child.Height = 10, 10, 40, 40
	child.FillColor = "#FF00FF00"

	group := model.NewGroupItem("Cluster")
	group.Add(child)

	g := newSurface(100, 100)
	Render(g, model.ItemList{group}, Frame{Profile: profile})

	if countPainted(g, color.NRGBA{0, 0, 0, 255}) == 0 {
		t.Error("a group's children were not drawn")
	}
}

func TestProfileBackgroundIsPainted(t *testing.T) {
	profile := model.NewProfile("Test", 20, 20)
	profile.BackgroundColor = "#FF102030"

	g := newSurface(20, 20)
	Render(g, nil, Frame{Profile: profile})

	r, gr, b, a := g.Image().At(10, 10).RGBA()
	if uint8(r>>8) != 0x10 || uint8(gr>>8) != 0x20 || uint8(b>>8) != 0x30 || uint8(a>>8) != 0xFF {
		t.Errorf("background = %d,%d,%d,%d; want 16,32,48,255", r>>8, gr>>8, b>>8, a>>8)
	}
}

func TestDesignGridOnlyDrawsInDesignMode(t *testing.T) {
	profile := model.NewProfile("Test", 100, 100)
	profile.BackgroundColor = "#FF000000"

	frame := Frame{
		Profile:     profile,
		GridSpacing: 10,
		GridColor:   color.NRGBA{255, 255, 255, 255},
	}

	live := newSurface(100, 100)
	Render(live, nil, frame)
	if got := countPainted(live, color.NRGBA{0, 0, 0, 255}); got != 0 {
		t.Errorf("the grid painted %d pixels outside design mode, want 0", got)
	}

	frame.Design = true
	designing := newSurface(100, 100)
	Render(designing, nil, frame)
	if countPainted(designing, color.NRGBA{0, 0, 0, 255}) == 0 {
		t.Error("the grid painted nothing in design mode")
	}
}

func TestSelectionOutlineOnlyDrawsForSelectedItems(t *testing.T) {
	profile := model.NewProfile("Test", 120, 120)
	profile.BackgroundColor = "#FF000000"

	shape := model.NewShapeItem("Box", model.ShapeRectangle)
	shape.X, shape.Y, shape.Width, shape.Height = 20, 20, 40, 40
	shape.FillColor = "#FF808080"

	base := newSurface(120, 120)
	Render(base, model.ItemList{shape}, Frame{Profile: profile, Design: true})
	baseCount := countPainted(base, color.NRGBA{0, 0, 0, 255})

	outlined := newSurface(120, 120)
	Render(outlined, model.ItemList{shape}, Frame{
		Profile: profile, Design: true,
		Selected:       []string{shape.ID},
		SelectionColor: color.NRGBA{0, 255, 0, 255},
	})

	if countPainted(outlined, color.NRGBA{0, 0, 0, 255}) <= baseCount {
		t.Error("selecting an item did not add an outline")
	}
}

func TestSensorTextRendersLiveValues(t *testing.T) {
	profile := model.NewProfile("Test", 200, 60)
	profile.BackgroundColor = "#FF000000"

	item := model.NewSensorItem("CPU")
	item.Key = sensor.Key{Source: sensor.SourceNative, Path: "cpu/load"}
	item.X, item.Y = 10, 10
	item.FontSize, item.Color = 20, "#FFFFFFFF"

	g := newSurface(200, 60)
	Render(g, model.ItemList{item}, Frame{
		Profile: profile,
		Sensors: fixedSensors{sensor.Reading{Now: 42, Unit: "%"}},
	})

	if countPainted(g, color.NRGBA{0, 0, 0, 255}) == 0 {
		t.Error("a bound sensor readout painted nothing")
	}
}

func TestBarFillTracksValue(t *testing.T) {
	profile := model.NewProfile("Test", 120, 40)
	profile.BackgroundColor = "#FF000000"

	newBar := func() *model.BarItem {
		bar := model.NewBarItem("CPU")
		bar.Key = sensor.Key{Source: sensor.SourceNative, Path: "cpu/load"}
		bar.X, bar.Y, bar.Width, bar.Height = 10, 10, 100, 20
		bar.Color, bar.Background, bar.Frame, bar.Gradient = "#FFFF0000", false, false, false
		return bar
	}

	countAt := func(value float64) int {
		g := newSurface(120, 40)
		Render(g, model.ItemList{newBar()}, Frame{
			Profile: profile,
			Sensors: fixedSensors{sensor.Reading{Now: value}},
		})
		return countPainted(g, color.NRGBA{0, 0, 0, 255})
	}

	quarter, half, full := countAt(25), countAt(50), countAt(100)
	if !(quarter < half && half < full) {
		t.Errorf("bar fill did not grow with value: 25%%=%d, 50%%=%d, 100%%=%d", quarter, half, full)
	}
}

func TestChartRangeAutoTracksData(t *testing.T) {
	style := &model.ChartStyle{MinValue: 0, MaxValue: 100, AutoValue: true}
	samples := []float64{40, 45, 50, 42}

	min, max := chartRange(style, samples)
	if min != 40 || max != 50 {
		t.Errorf("auto range = %v..%v, want 40..50 from the data", min, max)
	}
}

func TestChartRangeUsesConfiguredBoundsWithoutAuto(t *testing.T) {
	style := &model.ChartStyle{MinValue: 0, MaxValue: 100}
	if min, max := chartRange(style, []float64{40, 50}); min != 0 || max != 100 {
		t.Errorf("range = %v..%v, want the configured 0..100", min, max)
	}
}

func TestChartRangeWidensDegenerateBounds(t *testing.T) {
	// A zero-width range would divide by zero when normalizing.
	style := &model.ChartStyle{MinValue: 50, MaxValue: 50}
	min, max := chartRange(style, nil)
	if max <= min {
		t.Errorf("range = %v..%v, want it widened to avoid a zero divisor", min, max)
	}
}

func TestNormalizeClamps(t *testing.T) {
	tests := []struct {
		value, want float64
	}{
		{-10, 0}, {0, 0}, {50, 0.5}, {100, 1}, {150, 1},
	}
	for _, tt := range tests {
		if got := normalize(tt.value, 0, 100); got != tt.want {
			t.Errorf("normalize(%v) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestHistoryStoreRateLimitsSamples(t *testing.T) {
	// Sampling on every frame must still produce a series whose axis is time.
	h := NewHistoryStore(time.Second, 10)
	key := sensor.Key{Source: sensor.SourceNative, Path: "cpu/load"}
	start := time.Unix(0, 0)

	h.Sample(key, 1, start)
	h.Sample(key, 2, start.Add(100*time.Millisecond))
	h.Sample(key, 3, start.Add(200*time.Millisecond))

	values, ok := h.Values(key, 10)
	if !ok {
		t.Fatal("no history recorded")
	}
	if len(values) != 1 {
		t.Errorf("recorded %d samples in 200ms at a 1s interval, want 1", len(values))
	}
}

func TestHistoryStoreKeepsSamplesInOrder(t *testing.T) {
	h := NewHistoryStore(time.Millisecond, 10)
	key := sensor.Key{Source: sensor.SourceNative, Path: "cpu/load"}
	start := time.Unix(0, 0)

	for i := 1; i <= 5; i++ {
		h.Sample(key, float64(i), start.Add(time.Duration(i)*time.Second))
	}

	values, ok := h.Values(key, 10)
	if !ok {
		t.Fatal("no history recorded")
	}
	for i, want := range []float64{1, 2, 3, 4, 5} {
		if values[i] != want {
			t.Errorf("sample %d = %v, want %v (oldest first)", i, values[i], want)
		}
	}
}

func TestHistoryStoreDiscardsOldestBeyondCapacity(t *testing.T) {
	h := NewHistoryStore(time.Millisecond, 3)
	key := sensor.Key{Source: sensor.SourceNative, Path: "cpu/load"}
	start := time.Unix(0, 0)

	for i := 1; i <= 6; i++ {
		h.Sample(key, float64(i), start.Add(time.Duration(i)*time.Second))
	}

	values, _ := h.Values(key, 10)
	if len(values) != 3 {
		t.Fatalf("kept %d samples, want the capacity of 3", len(values))
	}
	for i, want := range []float64{4, 5, 6} {
		if values[i] != want {
			t.Errorf("sample %d = %v, want %v (the three most recent)", i, values[i], want)
		}
	}
}

func TestHistoryStoreReportsUnknownSensors(t *testing.T) {
	h := NewHistoryStore(time.Millisecond, 10)
	if _, ok := h.Values(sensor.Key{Path: "nothing"}, 10); ok {
		t.Error("a sensor with no history reported values")
	}
}

func TestHistoryStoreRetainDropsUnusedSeries(t *testing.T) {
	h := NewHistoryStore(time.Millisecond, 10)
	kept := sensor.Key{Source: sensor.SourceNative, Path: "keep"}
	dropped := sensor.Key{Source: sensor.SourceNative, Path: "drop"}

	now := time.Unix(0, 0)
	h.Sample(kept, 1, now)
	h.Sample(dropped, 1, now)

	h.Retain(map[sensor.Key]bool{kept: true})

	if _, ok := h.Values(kept, 10); !ok {
		t.Error("Retain dropped a series it was told to keep")
	}
	if _, ok := h.Values(dropped, 10); ok {
		t.Error("Retain kept a series it was not told to keep")
	}
}

func TestSmootherSnapsOnFirstObservation(t *testing.T) {
	// Easing up from zero would make every panel visibly fill on startup.
	s := NewSmoother(30)
	if got := s.Step("item", 0.8); got != 0.8 {
		t.Errorf("first step = %v, want the target 0.8", got)
	}
}

func TestSmootherApproachesTargetOverCycles(t *testing.T) {
	// The decay is recomputed against the remaining gap on every step, so the
	// approach is asymptotic rather than landing exactly at the cycle count.
	// What matters is that a value is visually settled by then.
	const cycles = 30
	s := NewSmoother(cycles)
	s.Step("item", 0)

	var value float64
	previous := 0.0
	for i := 0; i < cycles; i++ {
		value = s.Step("item", 1)
		if value < previous {
			t.Fatalf("step %d moved away from the target: %v after %v", i, value, previous)
		}
		previous = value
	}

	if math.Abs(value-1) > 0.05 {
		t.Errorf("after %d cycles the value is %v, want it within 0.05 of 1", cycles, value)
	}
}

func TestSmootherMovesGraduallyNotInstantly(t *testing.T) {
	s := NewSmoother(30)
	s.Step("item", 0)

	if got := s.Step("item", 1); got >= 1 || got <= 0 {
		t.Errorf("one step from 0 toward 1 gave %v, want a partial move", got)
	}
}

func TestSmootherTracksItemsIndependently(t *testing.T) {
	s := NewSmoother(30)
	s.Step("a", 0)
	s.Step("b", 100)

	s.Step("a", 1)
	if got := s.Step("b", 100); got != 100 {
		t.Errorf("item b = %v, want it unaffected by item a", got)
	}
}

func TestSmootherDisabledPassesValuesThrough(t *testing.T) {
	s := NewSmoother(0)
	s.Step("item", 0)
	if got := s.Step("item", 1); got != 1 {
		t.Errorf("with smoothing disabled the step gave %v, want the target 1", got)
	}
}

func TestNilSmootherIsUsable(t *testing.T) {
	var s *Smoother
	if got := s.Step("item", 0.5); got != 0.5 {
		t.Errorf("a nil smoother returned %v, want the target passed through", got)
	}
}

func TestInterpolateSettlesToASimilarAbsoluteTolerance(t *testing.T) {
	// The decay targets an absolute tolerance rather than a fixed fraction, so
	// a hundred-unit jump and a one-unit nudge both end up close to their
	// target in absolute terms after the same number of cycles. That is what
	// keeps a bar sweeping to 100%% and one nudging by 1%% feeling alike.
	const cycles = 20

	near, far := 50.0, 0.0
	for i := 0; i < cycles; i++ {
		near = interpolate(near, 51, cycles)
		far = interpolate(far, 100, cycles)
	}

	nearGap := math.Abs(near - 51)
	farGap := math.Abs(far - 100)

	if nearGap > 0.1 {
		t.Errorf("the small move is still %v from its target, want under 0.1", nearGap)
	}
	if farGap > 0.1 {
		t.Errorf("the large move is still %v from its target, want under 0.1", farGap)
	}
}

func TestInterpolateReachesTargetInsideTolerance(t *testing.T) {
	// Once the gap is negligible the value snaps, so it does not creep
	// forever without ever arriving.
	if got := interpolate(0.9999, 1, 30); got != 1 {
		t.Errorf("interpolate within tolerance = %v, want it to snap to 1", got)
	}
}

func TestShapePointsCoverEveryPolygonalShape(t *testing.T) {
	area := graphics.Rect{X: 0, Y: 0, W: 100, H: 100}

	polygonal := []model.ShapeType{
		model.ShapeTriangle, model.ShapeTrapezoid, model.ShapeParallelogram,
		model.ShapePentagon, model.ShapeHexagon, model.ShapeOctagon,
		model.ShapeStar, model.ShapePlus, model.ShapeArrow,
	}

	for _, shape := range polygonal {
		points := shapePoints(shape, area)
		if len(points) < 3 {
			t.Errorf("%s produced %d points, want at least 3", shape, len(points))
			continue
		}
		for _, p := range points {
			if p.X < area.X-0.01 || p.X > area.X+area.W+0.01 ||
				p.Y < area.Y-0.01 || p.Y > area.Y+area.H+0.01 {
				t.Errorf("%s has a vertex at (%v, %v) outside its bounds", shape, p.X, p.Y)
			}
		}
	}
}

func TestEveryShapeTypeDrawsSomething(t *testing.T) {
	profile := model.NewProfile("Test", 80, 80)
	profile.BackgroundColor = "#FF000000"

	for _, shapeType := range model.ShapeTypes {
		shape := model.NewShapeItem(string(shapeType), shapeType)
		shape.X, shape.Y, shape.Width, shape.Height = 10, 10, 60, 60
		shape.FillColor = "#FFFFFFFF"

		g := newSurface(80, 80)
		Render(g, model.ItemList{shape}, Frame{Profile: profile})

		if countPainted(g, color.NRGBA{0, 0, 0, 255}) == 0 {
			t.Errorf("shape %q painted nothing", shapeType)
		}
	}
}

func TestRegularPolygonStaysInsideTheUnitSquare(t *testing.T) {
	for _, sides := range []int{3, 5, 6, 8} {
		for _, p := range regularPolygon(sides) {
			if p.X < -0.01 || p.X > 1.01 || p.Y < -0.01 || p.Y > 1.01 {
				t.Errorf("%d-sided polygon has a vertex at (%v, %v) outside the unit square",
					sides, p.X, p.Y)
			}
		}
	}
}

func TestImageRectUsesScaleWhenUnsized(t *testing.T) {
	item := model.NewImageItem("Logo")
	item.X, item.Y = 10, 20
	item.Scale = 50

	got := imageRect(item, 200, 100)
	if got.W != 100 || got.H != 50 {
		t.Errorf("imageRect = %vx%v, want the natural size halved to 100x50", got.W, got.H)
	}
}

func TestImageRectPrefersExplicitDimensions(t *testing.T) {
	item := model.NewImageItem("Logo")
	item.Width, item.Height = 64, 64
	item.Scale = 25

	got := imageRect(item, 200, 100)
	if got.W != 64 || got.H != 64 {
		t.Errorf("imageRect = %vx%v, want the explicit 64x64", got.W, got.H)
	}
}
