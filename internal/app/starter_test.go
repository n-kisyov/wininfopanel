package app

import (
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// newTestApp builds an application against a temporary data directory, with
// nothing that touches the desktop or spawns a process.
func newTestApp(t *testing.T) *App {
	t.Helper()
	a, err := New(Options{DataDir: t.TempDir(), NoOverlays: true, NoPlugins: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestEnsureDefaultProfileSeedsAStarterPanel(t *testing.T) {
	a := newTestApp(t)

	profile, err := a.EnsureDefaultProfile()
	if err != nil {
		t.Fatalf("EnsureDefaultProfile: %v", err)
	}

	// The whole point of the starter panel: a first run must not come up as a
	// blank white rectangle.
	if profile.BackgroundColor == "#FFFFFFFF" {
		t.Error("the default profile kept the opaque white background")
	}
	if profile.BackgroundColor != starterBackground {
		t.Errorf("BackgroundColor = %q, want %q", profile.BackgroundColor, starterBackground)
	}

	items, err := a.Store.Layout(profile.ID)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("the default profile was created with an empty layout")
	}
}

func TestStarterLayoutBindsOnlyToTheBuiltInMonitor(t *testing.T) {
	// HWiNFO is not running on most machines and no plugin is guaranteed to
	// exist, so a starter panel bound to either would come up showing "-".
	for _, item := range model.FlattenAll(starterLayout()) {
		bound, ok := item.(model.SensorBound)
		if !ok {
			continue
		}

		binding := bound.Binding()
		if !binding.IsBound() {
			t.Errorf("item %q carries a sensor binding that points nowhere", item.Base().Name)
			continue
		}
		if binding.Key.Source != sensor.SourceNative {
			t.Errorf("item %q binds to source %q, want %q",
				item.Base().Name, binding.Key.Source, sensor.SourceNative)
		}
	}
}

func TestStarterLayoutFitsTheDefaultCanvas(t *testing.T) {
	profile := model.NewProfile("Default", 800, 480)
	ctx := &model.EvalContext{Profile: profile}

	for _, item := range model.FlattenAll(starterLayout()) {
		bounds := item.Bounds(ctx)
		if bounds.Left < 0 || bounds.Top < 0 {
			t.Errorf("item %q starts off-canvas at (%.0f, %.0f)",
				item.Base().Name, bounds.Left, bounds.Top)
		}
		if bounds.Right > float64(profile.Width) {
			t.Errorf("item %q extends to x=%.0f, past the %d-wide canvas",
				item.Base().Name, bounds.Right, profile.Width)
		}
		if bounds.Bottom > float64(profile.Height) {
			t.Errorf("item %q extends to y=%.0f, past the %d-tall canvas",
				item.Base().Name, bounds.Bottom, profile.Height)
		}
	}
}

func TestEnsureDefaultProfileLeavesExistingProfilesAlone(t *testing.T) {
	a := newTestApp(t)

	existing := model.NewProfile("Imported", 640, 480)
	if err := a.Store.AddProfile(existing); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	profile, err := a.EnsureDefaultProfile()
	if err != nil {
		t.Fatalf("EnsureDefaultProfile: %v", err)
	}
	if profile.ID != existing.ID {
		t.Errorf("EnsureDefaultProfile returned %q, want the existing profile %q",
			profile.ID, existing.ID)
	}
	if got := len(a.Store.Profiles()); got != 1 {
		t.Errorf("Profiles() returned %d, want 1: no starter profile should be added", got)
	}
}
