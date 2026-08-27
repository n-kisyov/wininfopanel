// Package store loads and persists wininfopanel's settings and profiles.
//
// The on-disk layout mirrors InfoPanel's split: one settings file, one profile
// index, and one file per profile holding its display items. Keeping layouts
// in separate files means switching profiles does not require parsing every
// layout, and a corrupt layout costs one profile rather than all of them.
package store

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/logging"
)

const (
	settingsFileName = "settings.json"
	profilesFileName = "profiles.json"
	profilesDirName  = "profiles"
	assetsDirName    = "assets"
)

// Store is the authoritative in-memory copy of the configuration, backed by
// files under a root directory.
//
// It is safe for concurrent use. Readers that need the live layout — the
// render loop, most importantly — should use WithLayout to borrow it under a
// read lock rather than copying it every frame.
type Store struct {
	root string
	log  *slog.Logger

	mu       sync.RWMutex
	settings *model.Settings
	profiles []*model.Profile
	// layouts holds display items per profile ID. A profile absent from the
	// map has not been loaded yet; loading is lazy.
	layouts map[string]model.ItemList
}

// Open reads the configuration rooted at dir, creating defaults when the
// directory is empty.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("store root directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create store directory %s: %w", dir, err)
	}

	s := &Store{
		root:    dir,
		log:     logging.For("config.store"),
		layouts: make(map[string]model.ItemList),
	}

	s.loadSettings()
	s.loadProfiles()
	return s, nil
}

// Root returns the directory the store reads and writes.
func (s *Store) Root() string { return s.root }

func (s *Store) settingsPath() string { return filepath.Join(s.root, settingsFileName) }
func (s *Store) profilesPath() string { return filepath.Join(s.root, profilesFileName) }

func (s *Store) profilePath(id string) string {
	return filepath.Join(s.root, profilesDirName, id+".json")
}

// AssetsDir returns the asset directory belonging to a profile, creating it.
func (s *Store) AssetsDir(profileID string) (string, error) {
	dir := filepath.Join(s.root, assetsDirName, profileID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create asset directory %s: %w", dir, err)
	}
	return dir, nil
}

// loadSettings reads settings, falling back to defaults when the file is
// missing or unreadable. A configuration that will not parse should not stop
// the application from starting, but it must be reported.
func (s *Store) loadSettings() {
	settings := model.DefaultSettings()

	found, err := readJSON(s.settingsPath(), settings)
	switch {
	case errors.Is(err, ErrRecoveredFromBackup):
		// The data is good; only the primary file was damaged. Keep what was
		// recovered rather than throwing the user's configuration away.
		s.log.Warn("settings were recovered from the backup file", "error", err)
	case err != nil:
		s.log.Error("settings could not be read; continuing with defaults", "error", err)
		settings = model.DefaultSettings()
	case !found:
		s.log.Info("no settings file found; starting with defaults")
	}

	settings.Normalize()
	s.settings = settings
}

func (s *Store) loadProfiles() {
	var profiles []*model.Profile
	if _, err := readJSON(s.profilesPath(), &profiles); err != nil {
		if errors.Is(err, ErrRecoveredFromBackup) {
			s.log.Warn("profile index was recovered from the backup file", "error", err)
		} else {
			s.log.Error("profile index could not be read; starting with no profiles", "error", err)
			profiles = nil
		}
	}

	// Drop entries that cannot be addressed: an ID is what ties a profile to
	// its layout file and its asset directory.
	kept := make([]*model.Profile, 0, len(profiles))
	for _, p := range profiles {
		if p == nil || p.ID == "" {
			s.log.Warn("skipping a profile with no id")
			continue
		}
		kept = append(kept, p)
	}
	s.profiles = kept
}

// Settings returns a copy of the current settings.
func (s *Store) Settings() model.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.settings
}

// UpdateSettings applies fn to the settings under lock, normalizes the result,
// and persists it.
func (s *Store) UpdateSettings(fn func(*model.Settings)) error {
	s.mu.Lock()
	fn(s.settings)
	s.settings.Normalize()
	snapshot := *s.settings
	s.mu.Unlock()

	return writeJSONAtomic(s.settingsPath(), &snapshot)
}

// SaveSettings persists the current settings without modifying them.
func (s *Store) SaveSettings() error {
	s.mu.RLock()
	snapshot := *s.settings
	s.mu.RUnlock()
	return writeJSONAtomic(s.settingsPath(), &snapshot)
}

// Profiles returns copies of every profile, in order.
func (s *Store) Profiles() []*model.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*model.Profile, len(s.profiles))
	for i, p := range s.profiles {
		clone := *p
		out[i] = &clone
	}
	return out
}

// Profile returns a copy of one profile.
func (s *Store) Profile(id string) (*model.Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, p := range s.profiles {
		if p.ID == id {
			clone := *p
			return &clone, true
		}
	}
	return nil, false
}

// AddProfile appends a profile and persists both the index and an empty
// layout, so every profile in the index has a layout file behind it.
func (s *Store) AddProfile(p *model.Profile) error {
	if p == nil || p.ID == "" {
		return fmt.Errorf("profile must have an id")
	}

	s.mu.Lock()
	for _, existing := range s.profiles {
		if existing.ID == p.ID {
			s.mu.Unlock()
			return fmt.Errorf("profile %s already exists", p.ID)
		}
	}
	clone := *p
	s.profiles = append(s.profiles, &clone)
	if _, ok := s.layouts[p.ID]; !ok {
		s.layouts[p.ID] = nil
	}
	s.mu.Unlock()

	if err := s.saveProfileIndex(); err != nil {
		return err
	}
	return s.SaveLayout(p.ID)
}

// UpdateProfile applies fn to a profile under lock and persists the index.
func (s *Store) UpdateProfile(id string, fn func(*model.Profile)) error {
	s.mu.Lock()
	var target *model.Profile
	for _, p := range s.profiles {
		if p.ID == id {
			target = p
			break
		}
	}
	if target == nil {
		s.mu.Unlock()
		return fmt.Errorf("profile %s not found", id)
	}
	fn(target)
	s.mu.Unlock()

	return s.saveProfileIndex()
}

// RemoveProfile deletes a profile along with its layout file and assets.
func (s *Store) RemoveProfile(id string) error {
	s.mu.Lock()
	index := -1
	for i, p := range s.profiles {
		if p.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		s.mu.Unlock()
		return fmt.Errorf("profile %s not found", id)
	}
	s.profiles = append(s.profiles[:index], s.profiles[index+1:]...)
	delete(s.layouts, id)
	s.mu.Unlock()

	if err := s.saveProfileIndex(); err != nil {
		return err
	}

	// Removing the on-disk remnants is best effort: the profile is already
	// out of the index, and a file held open elsewhere should not turn a
	// successful delete into a failure.
	for _, path := range []string{s.profilePath(id), s.profilePath(id) + ".bak"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			s.log.Warn("could not remove profile file", "path", path, "error", err)
		}
	}
	assets := filepath.Join(s.root, assetsDirName, id)
	if err := os.RemoveAll(assets); err != nil {
		s.log.Warn("could not remove profile assets", "path", assets, "error", err)
	}
	return nil
}

// ReorderProfiles rewrites the profile order to match the given IDs. Profiles
// not named keep their existing relative order and move to the end.
func (s *Store) ReorderProfiles(ids []string) error {
	s.mu.Lock()
	position := make(map[string]int, len(ids))
	for i, id := range ids {
		position[id] = i
	}

	ordered := make([]*model.Profile, 0, len(s.profiles))
	remainder := make([]*model.Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		if _, ok := position[p.ID]; ok {
			ordered = append(ordered, p)
		} else {
			remainder = append(remainder, p)
		}
	}
	sortByPosition(ordered, position)
	s.profiles = append(ordered, remainder...)
	s.mu.Unlock()

	return s.saveProfileIndex()
}

// sortByPosition orders profiles by their index in position. Insertion sort
// keeps it stable, and profile counts are small enough that nothing better is
// warranted.
func sortByPosition(profiles []*model.Profile, position map[string]int) {
	for i := 1; i < len(profiles); i++ {
		current := profiles[i]
		j := i - 1
		for j >= 0 && position[profiles[j].ID] > position[current.ID] {
			profiles[j+1] = profiles[j]
			j--
		}
		profiles[j+1] = current
	}
}

func (s *Store) saveProfileIndex() error {
	s.mu.RLock()
	snapshot := make([]*model.Profile, len(s.profiles))
	for i, p := range s.profiles {
		clone := *p
		snapshot[i] = &clone
	}
	s.mu.RUnlock()

	return writeJSONAtomic(s.profilesPath(), snapshot)
}
