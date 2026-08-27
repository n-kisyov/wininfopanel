package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
)

// Layouts are loaded on demand: a user with twenty profiles pays for parsing
// only the ones actually shown. Once loaded a layout stays cached until the
// profile is removed.

// ensureLayoutLoaded reads a profile's display items from disk if they are not
// already cached. The caller must hold s.mu for writing.
func (s *Store) ensureLayoutLoaded(profileID string) error {
	if _, ok := s.layouts[profileID]; ok {
		return nil
	}

	var items model.ItemList
	found, err := readJSON(s.profilePath(profileID), &items)
	if err != nil {
		// Cache the empty result so a broken layout is reported once rather
		// than on every frame.
		s.layouts[profileID] = nil
		return fmt.Errorf("load layout for profile %s: %w", profileID, err)
	}
	if !found {
		items = nil
	}

	s.layouts[profileID] = items
	return nil
}

// Layout returns a deep copy of a profile's display items.
//
// Prefer WithLayout on hot paths: this copies the whole tree.
func (s *Store) Layout(profileID string) (model.ItemList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureLayoutLoaded(profileID); err != nil {
		return nil, err
	}
	return model.CloneAll(s.layouts[profileID]), nil
}

// WithLayout calls fn with the live display items under a read lock.
//
// fn must not retain or mutate the slice: it is the store's own copy, borrowed
// to avoid cloning the tree on every render. Use Mutate to make changes.
func (s *Store) WithLayout(profileID string, fn func(model.ItemList)) error {
	// Fast path: an already-cached layout needs only a read lock, which is
	// what the render loop hits on every frame.
	s.mu.RLock()
	if items, ok := s.layouts[profileID]; ok {
		defer s.mu.RUnlock()
		fn(items)
		return nil
	}
	s.mu.RUnlock()

	// Slow path: loading mutates the cache, so hold the write lock for the
	// whole operation. Releasing it before calling fn would let another writer
	// swap the slice out from under the borrow.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLayoutLoaded(profileID); err != nil {
		return err
	}
	fn(s.layouts[profileID])
	return nil
}

// SetLayout replaces a profile's display items and persists them.
func (s *Store) SetLayout(profileID string, items model.ItemList) error {
	s.mu.Lock()
	s.layouts[profileID] = items
	s.mu.Unlock()

	return s.SaveLayout(profileID)
}

// Mutate applies fn to a profile's display items under a write lock and
// persists the result.
//
// fn returns the new item list, which lets it replace the whole layout as well
// as edit items in place.
func (s *Store) Mutate(profileID string, fn func(model.ItemList) model.ItemList) error {
	s.mu.Lock()
	if err := s.ensureLayoutLoaded(profileID); err != nil {
		s.mu.Unlock()
		return err
	}
	s.layouts[profileID] = fn(s.layouts[profileID])
	s.mu.Unlock()

	return s.SaveLayout(profileID)
}

// SaveLayout persists a profile's display items.
func (s *Store) SaveLayout(profileID string) error {
	s.mu.RLock()
	items, loaded := s.layouts[profileID]
	s.mu.RUnlock()

	if !loaded {
		// Nothing has been read or written for this profile, so there is
		// nothing to persist. Writing here would clobber an on-disk layout
		// that simply has not been loaded yet.
		return nil
	}

	if items == nil {
		items = model.ItemList{}
	}
	return writeJSONAtomic(s.profilePath(profileID), items)
}

// SaveAll persists settings, the profile index, and every loaded layout.
func (s *Store) SaveAll() error {
	if err := s.SaveSettings(); err != nil {
		return err
	}
	if err := s.saveProfileIndex(); err != nil {
		return err
	}

	s.mu.RLock()
	ids := make([]string, 0, len(s.layouts))
	for id := range s.layouts {
		ids = append(ids, id)
	}
	s.mu.RUnlock()

	for _, id := range ids {
		if err := s.SaveLayout(id); err != nil {
			return err
		}
	}
	return nil
}

// PruneOrphans removes layout files and asset directories belonging to
// profiles that no longer exist.
//
// InfoPanel does this on every profile save; here it is explicit so it can run
// once at startup rather than on every write.
func (s *Store) PruneOrphans() error {
	s.mu.RLock()
	live := make(map[string]bool, len(s.profiles))
	for _, p := range s.profiles {
		live[p.ID] = true
	}
	s.mu.RUnlock()

	profilesDir := filepath.Join(s.root, profilesDirName)

	if entries, err := os.ReadDir(profilesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			id := trimSuffixes(e.Name(), ".bak", ".json")
			if id == "" || live[id] {
				continue
			}
			path := filepath.Join(profilesDir, e.Name())
			if err := os.Remove(path); err != nil {
				s.log.Warn("could not prune orphaned layout", "path", path, "error", err)
			}
		}
	}

	assetsRoot := filepath.Join(s.root, assetsDirName)
	if entries, err := os.ReadDir(assetsRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() || live[e.Name()] {
				continue
			}
			path := filepath.Join(assetsRoot, e.Name())
			if err := os.RemoveAll(path); err != nil {
				s.log.Warn("could not prune orphaned assets", "path", path, "error", err)
			}
		}
	}
	return nil
}

// trimSuffixes strips each suffix from name, in order, when present.
func trimSuffixes(name string, suffixes ...string) string {
	for _, suffix := range suffixes {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}
