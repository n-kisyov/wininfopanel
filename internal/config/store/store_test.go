package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestOpenOnEmptyDirectoryYieldsDefaults(t *testing.T) {
	s := newTestStore(t)

	settings := s.Settings()
	if settings.Theme != model.ThemeDark {
		t.Errorf("Theme = %q, want %q", settings.Theme, model.ThemeDark)
	}
	if settings.TargetFrameRate != 15 {
		t.Errorf("TargetFrameRate = %d, want 15", settings.TargetFrameRate)
	}
	if got := len(s.Profiles()); got != 0 {
		t.Errorf("Profiles() returned %d, want 0 on a fresh store", got)
	}
}

func TestSettingsPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.UpdateSettings(func(st *model.Settings) {
		st.TargetFrameRate = 30
		st.WebServer.Port = 9000
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	settings := reopened.Settings()
	if settings.TargetFrameRate != 30 {
		t.Errorf("TargetFrameRate = %d, want 30", settings.TargetFrameRate)
	}
	if settings.WebServer.Port != 9000 {
		t.Errorf("WebServer.Port = %d, want 9000", settings.WebServer.Port)
	}
}

func TestUpdateSettingsNormalizesOutOfRangeValues(t *testing.T) {
	s := newTestStore(t)

	if err := s.UpdateSettings(func(st *model.Settings) {
		st.TargetFrameRate = 0
		st.WebServer.Port = 99999
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	settings := s.Settings()
	if settings.TargetFrameRate != 15 {
		t.Errorf("TargetFrameRate = %d, want the default 15 after normalizing 0", settings.TargetFrameRate)
	}
	if settings.WebServer.Port != 8080 {
		t.Errorf("WebServer.Port = %d, want the default 8080 after normalizing 99999", settings.WebServer.Port)
	}
}

func TestSettingsAreACopyNotAliasedState(t *testing.T) {
	s := newTestStore(t)

	settings := s.Settings()
	settings.TargetFrameRate = 999
	if settings.TargetFrameRate != 999 {
		t.Fatal("the local copy did not take the write")
	}

	if s.Settings().TargetFrameRate == 999 {
		t.Error("mutating the returned settings changed the store's own state")
	}
}

func TestProfileLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	profile := model.NewProfile("Main", 800, 480)
	if err := s.AddProfile(profile); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	if _, ok := s.Profile(profile.ID); !ok {
		t.Fatal("profile not found after AddProfile")
	}

	if err := s.UpdateProfile(profile.ID, func(p *model.Profile) { p.Name = "Renamed" }); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.Profile(profile.ID)
	if !ok {
		t.Fatal("profile did not survive a reopen")
	}
	if got.Name != "Renamed" {
		t.Errorf("Name = %q, want %q", got.Name, "Renamed")
	}

	if err := reopened.RemoveProfile(profile.ID); err != nil {
		t.Fatalf("RemoveProfile: %v", err)
	}
	if _, ok := reopened.Profile(profile.ID); ok {
		t.Error("profile still present after RemoveProfile")
	}
	if _, err := os.Stat(filepath.Join(dir, profilesDirName, profile.ID+".json")); !os.IsNotExist(err) {
		t.Error("layout file was not removed with the profile")
	}
}

func TestAddProfileRejectsDuplicateID(t *testing.T) {
	s := newTestStore(t)
	profile := model.NewProfile("Main", 800, 480)

	if err := s.AddProfile(profile); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := s.AddProfile(profile); err == nil {
		t.Error("expected an error when adding a profile with an existing id")
	}
}

func TestAddProfileRejectsMissingID(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddProfile(&model.Profile{Name: "No ID"}); err == nil {
		t.Error("expected an error for a profile with no id")
	}
}

func TestLayoutPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	profile := model.NewProfile("Main", 800, 480)
	if err := s.AddProfile(profile); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	items := model.ItemList{
		model.NewTextItem("Hello"),
		model.NewBarItem("CPU"),
	}
	if err := s.SetLayout(profile.ID, items); err != nil {
		t.Fatalf("SetLayout: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	loaded, err := reopened.Layout(profile.ID)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d items, want 2", len(loaded))
	}
	if loaded[0].Kind() != model.KindText || loaded[1].Kind() != model.KindBar {
		t.Errorf("kinds = %s, %s; want Text, Bar", loaded[0].Kind(), loaded[1].Kind())
	}
	if loaded[0].Base().Name != "Hello" {
		t.Errorf("first item name = %q, want %q", loaded[0].Base().Name, "Hello")
	}
}

func TestMutateAppliesAndPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	profile := model.NewProfile("Main", 800, 480)
	if err := s.AddProfile(profile); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	if err := s.Mutate(profile.ID, func(items model.ItemList) model.ItemList {
		return append(items, model.NewShapeItem("Box", model.ShapeRectangle))
	}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	loaded, err := reopened.Layout(profile.ID)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Kind() != model.KindShape {
		t.Errorf("layout = %v, want one shape item", loaded)
	}
}

func TestLayoutReturnsIndependentCopy(t *testing.T) {
	s := newTestStore(t)
	profile := model.NewProfile("Main", 800, 480)
	if err := s.AddProfile(profile); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := s.SetLayout(profile.ID, model.ItemList{model.NewTextItem("Original")}); err != nil {
		t.Fatalf("SetLayout: %v", err)
	}

	copy1, err := s.Layout(profile.ID)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	copy1[0].Base().Name = "Mutated"

	copy2, err := s.Layout(profile.ID)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if copy2[0].Base().Name != "Original" {
		t.Error("mutating a returned layout changed the store's own state")
	}
}

func TestSaveLayoutDoesNotClobberUnloadedLayout(t *testing.T) {
	// A profile whose layout has never been read must not have its on-disk
	// file overwritten with an empty list.
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	profile := model.NewProfile("Main", 800, 480)
	if err := s.AddProfile(profile); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := s.SetLayout(profile.ID, model.ItemList{model.NewTextItem("Keep me")}); err != nil {
		t.Fatalf("SetLayout: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := reopened.SaveLayout(profile.ID); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	loaded, err := reopened.Layout(profile.ID)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("layout has %d items after saving an unloaded profile, want 1", len(loaded))
	}
}

func TestReorderProfiles(t *testing.T) {
	s := newTestStore(t)
	var ids []string
	for _, name := range []string{"A", "B", "C"} {
		p := model.NewProfile(name, 100, 100)
		if err := s.AddProfile(p); err != nil {
			t.Fatalf("AddProfile(%s): %v", name, err)
		}
		ids = append(ids, p.ID)
	}

	if err := s.ReorderProfiles([]string{ids[2], ids[0], ids[1]}); err != nil {
		t.Fatalf("ReorderProfiles: %v", err)
	}

	got := s.Profiles()
	want := []string{"C", "A", "B"}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("position %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestPruneOrphansRemovesStrayLayoutsAndAssets(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	live := model.NewProfile("Live", 100, 100)
	if err := s.AddProfile(live); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if _, err := s.AssetsDir(live.ID); err != nil {
		t.Fatalf("AssetsDir: %v", err)
	}

	orphanLayout := filepath.Join(dir, profilesDirName, "ghost.json")
	if err := os.WriteFile(orphanLayout, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphanAssets := filepath.Join(dir, assetsDirName, "ghost")
	if err := os.MkdirAll(orphanAssets, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := s.PruneOrphans(); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}

	if _, err := os.Stat(orphanLayout); !os.IsNotExist(err) {
		t.Error("orphaned layout file was not pruned")
	}
	if _, err := os.Stat(orphanAssets); !os.IsNotExist(err) {
		t.Error("orphaned asset directory was not pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, profilesDirName, live.ID+".json")); err != nil {
		t.Errorf("pruning removed a live profile's layout: %v", err)
	}
}

func TestCorruptSettingsFallBackToBackup(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Two saves: the first creates settings.json, the second rotates it to
	// settings.json.bak so a backup exists to fall back to.
	if err := s.UpdateSettings(func(st *model.Settings) { st.TargetFrameRate = 42 }); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := s.UpdateSettings(func(st *model.Settings) { st.TargetFrameRate = 42 }); err != nil {
		t.Fatalf("second save: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, settingsFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.Settings().TargetFrameRate; got != 42 {
		t.Errorf("TargetFrameRate = %d, want 42 recovered from the backup", got)
	}
}
