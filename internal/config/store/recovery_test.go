package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
)

// These cover the ways the store used to lose or leak the user's work. Each
// one failed before the fix it names, so each is here to stop it coming back
// rather than to describe an API.

// seedProfile writes a profile with a layout and one asset, and returns its ID
// along with the asset's path.
func seedProfile(t *testing.T, s *Store, name string) (string, string) {
	t.Helper()

	p := model.NewProfile(name, 800, 480)
	if err := s.AddProfile(p); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := s.SetLayout(p.ID, model.ItemList{model.NewClockItem()}); err != nil {
		t.Fatalf("SetLayout: %v", err)
	}

	assets, err := s.AssetsDir(p.ID)
	if err != nil {
		t.Fatalf("AssetsDir: %v", err)
	}
	asset := filepath.Join(assets, "logo.png")
	if err := os.WriteFile(asset, []byte("png"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	return p.ID, asset
}

// corrupt makes a file and its backup unparseable, the way a bad shutdown or a
// failing disk can.
func corrupt(t *testing.T, path string) {
	t.Helper()
	for _, p := range []string{path, path + ".bak"} {
		if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
			t.Fatalf("corrupt %s: %v", p, err)
		}
	}
}

// A corrupt profile index used to leave the store with an empty profile list,
// which PruneOrphans then read as "every layout and asset on disk is an
// orphan" and deleted the lot.
func TestCorruptProfileIndexDoesNotDestroyLayouts(t *testing.T) {
	dir := t.TempDir()

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, asset := seedProfile(t, first, "Real work")
	layout := first.profilePath(id)

	corrupt(t, first.profilesPath())

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !reopened.ProfilesLoadFailed() {
		t.Fatal("the store did not report that the profile index failed to load")
	}
	if err := reopened.PruneOrphans(); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}

	if _, err := os.Stat(layout); err != nil {
		t.Errorf("the layout was lost after a corrupt profile index: %v", err)
	}
	if _, err := os.Stat(asset); err != nil {
		t.Errorf("the profile's assets were lost after a corrupt profile index: %v", err)
	}

	// The damaged index is the only record of what the layouts were called, so
	// it must survive the saves that are about to overwrite it.
	if !quarantineHolds(t, dir, profilesFileName) {
		t.Error("the unreadable profile index was not preserved")
	}
}

// The second guard, independent of the flag: if nothing is loaded but files
// are on disk, the index is not to be trusted whatever it parsed as.
func TestPruneRefusesWhenNoProfilesAreLoadedButFilesRemain(t *testing.T) {
	dir := t.TempDir()

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, asset := seedProfile(t, first, "Real work")
	layout := first.profilePath(id)

	// An index that parses but is empty -- a truncated write, or a hand edit.
	if err := os.WriteFile(first.profilesPath(), []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("empty the index: %v", err)
	}
	os.Remove(first.profilesPath() + ".bak")

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.ProfilesLoadFailed() {
		t.Fatal("an empty but valid index should not count as a load failure")
	}
	if err := reopened.PruneOrphans(); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}

	if _, err := os.Stat(layout); err != nil {
		t.Errorf("the layout was lost to an empty profile index: %v", err)
	}
	if _, err := os.Stat(asset); err != nil {
		t.Errorf("the assets were lost to an empty profile index: %v", err)
	}
}

// A genuine orphan is still cleared away -- but recoverably.
func TestPruneQuarantinesGenuineOrphans(t *testing.T) {
	dir := t.TempDir()

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	keptID, keptAsset := seedProfile(t, s, "Keep me")

	// A layout and an asset directory belonging to no profile at all.
	orphanID := "11111111-2222-3333-4444-555555555555"
	orphanLayout := s.profilePath(orphanID)
	if err := os.WriteFile(orphanLayout, []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("write orphan layout: %v", err)
	}
	orphanAssets := filepath.Join(dir, assetsDirName, orphanID)
	if err := os.MkdirAll(orphanAssets, 0o755); err != nil {
		t.Fatalf("make orphan assets: %v", err)
	}

	if err := s.PruneOrphans(); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}

	if _, err := os.Stat(orphanLayout); !os.IsNotExist(err) {
		t.Error("the orphaned layout was left in place")
	}
	if _, err := os.Stat(orphanAssets); !os.IsNotExist(err) {
		t.Error("the orphaned assets were left in place")
	}

	if _, err := os.Stat(s.profilePath(keptID)); err != nil {
		t.Errorf("a live profile's layout was pruned: %v", err)
	}
	if _, err := os.Stat(keptAsset); err != nil {
		t.Errorf("a live profile's assets were pruned: %v", err)
	}

	// Recoverable, not gone: the point of quarantining rather than deleting.
	if !quarantineHolds(t, dir, orphanID) {
		t.Error("the orphans were deleted rather than quarantined")
	}
}

// quarantineHolds reports whether anything named for id landed in quarantine.
func quarantineHolds(t *testing.T, root, id string) bool {
	t.Helper()

	found := false
	err := filepath.WalkDir(filepath.Join(root, quarantineDirName),
		func(path string, _ os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if strings.Contains(filepath.Base(path), id) {
				found = true
			}
			return nil
		})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk quarantine: %v", err)
	}
	return found
}

// Settings() used to return a shallow copy, so editing a returned value's
// panel or hotkey list wrote straight into the store, off the lock and without
// persisting.
func TestSettingsCopyIsIndependent(t *testing.T) {
	s := newTestStore(t)

	err := s.UpdateSettings(func(st *model.Settings) {
		st.BeadaPanels = []model.PanelDevice{{ID: "a", Brightness: 50}}
		st.Hotkeys = []model.HotkeyBinding{{
			ID: "h", Key: "F9", Modifiers: []string{"ctrl"},
		}}
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	got := s.Settings()
	got.BeadaPanels[0].Brightness = 999
	got.Hotkeys[0].Modifiers[0] = "alt"

	again := s.Settings()
	if again.BeadaPanels[0].Brightness != 50 {
		t.Errorf("panel brightness is %d; editing a returned copy reached the store",
			again.BeadaPanels[0].Brightness)
	}
	if again.Hotkeys[0].Modifiers[0] != "ctrl" {
		t.Errorf("hotkey modifier is %q; editing a returned copy reached the store",
			again.Hotkeys[0].Modifiers[0])
	}
}

// The same defect on the profile side, through TargetWindow.
func TestProfileCopyIsIndependent(t *testing.T) {
	s := newTestStore(t)

	p := model.NewProfile("P", 800, 480)
	p.TargetWindow = &model.TargetWindow{X: 10, DeviceName: `\\.\DISPLAY1`}
	if err := s.AddProfile(p); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	got, ok := s.Profile(p.ID)
	if !ok {
		t.Fatal("the profile went missing")
	}
	got.TargetWindow.X = 999

	again, _ := s.Profile(p.ID)
	if again.TargetWindow.X != 10 {
		t.Errorf("TargetWindow.X is %d; editing a returned copy reached the store",
			again.TargetWindow.X)
	}

	// Profiles() takes the same path and used to share the same pointer.
	all := s.Profiles()
	all[0].TargetWindow.X = 777
	if final, _ := s.Profile(p.ID); final.TargetWindow.X != 10 {
		t.Errorf("TargetWindow.X is %d after editing a Profiles() result",
			final.TargetWindow.X)
	}
}

// AddProfile stores its own copy, so the caller keeping the profile it passed
// in cannot edit the store through it either.
func TestAddProfileCopiesItsArgument(t *testing.T) {
	s := newTestStore(t)

	p := model.NewProfile("P", 800, 480)
	p.TargetWindow = &model.TargetWindow{X: 10}
	if err := s.AddProfile(p); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	p.Name = "edited after the fact"
	p.TargetWindow.X = 999

	stored, _ := s.Profile(p.ID)
	if stored.Name != "P" || stored.TargetWindow.X != 10 {
		t.Errorf("the store followed edits to the caller's profile: name=%q x=%d",
			stored.Name, stored.TargetWindow.X)
	}
}

// Concurrent saves of one file used to be able to rotate a half-written copy
// into the ".bak" slot, leaving both files damaged. Run under -race.
func TestConcurrentSavesLeaveBothFilesParseable(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 8; i++ {
		if err := s.AddProfile(model.NewProfile("P", 800, 480)); err != nil {
			t.Fatalf("AddProfile: %v", err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SaveAll(); err != nil {
				t.Errorf("SaveAll: %v", err)
			}
		}()
	}
	wg.Wait()

	for _, path := range []string{s.profilesPath(), s.profilesPath() + ".bak"} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue // the backup only exists from the second save on
		}
		var profiles []*model.Profile
		if err := readJSONFile(path, &profiles); err != nil {
			t.Errorf("%s did not parse after concurrent saves: %v",
				filepath.Base(path), err)
		}
	}
}
