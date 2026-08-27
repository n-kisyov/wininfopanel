package winapi

import "testing"

func TestScreensReportsAtLeastOneDisplay(t *testing.T) {
	screens, err := Screens()
	if err != nil {
		t.Fatalf("Screens: %v", err)
	}
	if len(screens) == 0 {
		t.Fatal("no displays enumerated; a running desktop session must have at least one")
	}

	for _, s := range screens {
		if s.DeviceName == "" {
			t.Error("a display reported no device name; profiles key on it")
		}
		if s.Bounds.Width() <= 0 || s.Bounds.Height() <= 0 {
			t.Errorf("%s has empty bounds %+v", s.DeviceName, s.Bounds)
		}
		if s.DPI <= 0 {
			t.Errorf("%s reported DPI %d, want a positive value", s.DeviceName, s.DPI)
		}
	}
}

func TestExactlyOnePrimaryDisplay(t *testing.T) {
	screens, err := Screens()
	if err != nil {
		t.Fatalf("Screens: %v", err)
	}

	primaries := 0
	for _, s := range screens {
		if s.Primary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Errorf("%d displays are marked primary, want exactly 1", primaries)
	}
}

func TestPrimaryScreenIsAmongScreens(t *testing.T) {
	primary, err := PrimaryScreen()
	if err != nil {
		t.Fatalf("PrimaryScreen: %v", err)
	}
	if _, ok := ScreenByName(primary.DeviceName); !ok {
		t.Errorf("the primary display %q is not in the enumeration", primary.DeviceName)
	}
}

func TestWorkAreaFitsInsideBounds(t *testing.T) {
	// The work area excludes the taskbar, so it can never exceed the display.
	screens, err := Screens()
	if err != nil {
		t.Fatalf("Screens: %v", err)
	}

	for _, s := range screens {
		if s.WorkArea.Left < s.Bounds.Left || s.WorkArea.Top < s.Bounds.Top ||
			s.WorkArea.Right > s.Bounds.Right || s.WorkArea.Bottom > s.Bounds.Bottom {
			t.Errorf("%s work area %+v is not inside bounds %+v",
				s.DeviceName, s.WorkArea, s.Bounds)
		}
	}
}

func TestScreenAtFallsBackToPrimary(t *testing.T) {
	// A saved overlay position on an unplugged monitor lands far outside every
	// display; it must resolve to something rather than failing.
	screen, err := ScreenAt(-100000, -100000)
	if err != nil {
		t.Fatalf("ScreenAt: %v", err)
	}
	if !screen.Primary {
		t.Errorf("an off-screen point resolved to %q, want the primary display", screen.DeviceName)
	}
}

func TestClampToScreensLeavesVisiblePositionsAlone(t *testing.T) {
	primary, err := PrimaryScreen()
	if err != nil {
		t.Fatalf("PrimaryScreen: %v", err)
	}

	x := primary.WorkArea.Left + 100
	y := primary.WorkArea.Top + 100

	gotX, gotY := ClampToScreens(x, y, 400, 300)
	if gotX != x || gotY != y {
		t.Errorf("ClampToScreens moved a visible position from %d,%d to %d,%d", x, y, gotX, gotY)
	}
}

func TestClampToScreensRecoversOffscreenPositions(t *testing.T) {
	// Without this an overlay restored from a stale profile would render
	// somewhere the user cannot see and look like it failed to start.
	x, y := ClampToScreens(-50000, -50000, 400, 300)

	if _, err := ScreenAt(x, y); err != nil {
		t.Fatalf("ScreenAt on the clamped position: %v", err)
	}

	screens, err := Screens()
	if err != nil {
		t.Fatal(err)
	}
	visible := false
	for _, s := range screens {
		if s.Bounds.Contains(x, y) {
			visible = true
			break
		}
	}
	if !visible {
		t.Errorf("clamped position %d,%d is still not on any display", x, y)
	}
}

func TestRectangleGeometry(t *testing.T) {
	r := Rectangle{Left: 10, Top: 20, Right: 110, Bottom: 220}

	if r.Width() != 100 {
		t.Errorf("Width = %d, want 100", r.Width())
	}
	if r.Height() != 200 {
		t.Errorf("Height = %d, want 200", r.Height())
	}
	if !r.Contains(10, 20) {
		t.Error("Contains excluded the top-left corner, which is inclusive")
	}
	if r.Contains(110, 220) {
		t.Error("Contains included the bottom-right corner, which is exclusive")
	}
	if r.Contains(5, 20) {
		t.Error("Contains included a point left of the rectangle")
	}
}
