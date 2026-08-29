package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// quarantineDirName holds what PruneOrphans took away.
//
// Pruning is driven entirely by the profile index, so a wrong index means the
// wrong files. Moving rather than deleting means that mistake stays
// recoverable: the user gets their panels back by hand instead of not at all.
const quarantineDirName = "quarantine"

// PruneOrphans moves layout files and asset directories belonging to profiles
// that no longer exist into the quarantine directory.
//
// InfoPanel does this on every profile save; here it is explicit so it can run
// once at startup rather than on every write.
//
// "Belonging to no profile" is decided from the index in memory, so this is
// only safe when that index is known to be accurate. Both guards below exist
// because it once was not: an unparseable profiles.json left the store with an
// empty list, and every layout and asset in the installation looked orphaned.
func (s *Store) PruneOrphans() error {
	s.mu.RLock()
	failed := s.profilesLoadFailed
	live := make(map[string]bool, len(s.profiles))
	for _, p := range s.profiles {
		live[p.ID] = true
	}
	s.mu.RUnlock()

	if failed {
		s.log.Warn("not pruning orphans: the profile index could not be read, " +
			"so every layout and asset would look orphaned")
		return nil
	}

	profilesDir := filepath.Join(s.root, profilesDirName)
	assetsRoot := filepath.Join(s.root, assetsDirName)

	layouts := orphansIn(profilesDir, func(e os.DirEntry) (string, bool) {
		if e.IsDir() {
			return "", false
		}
		id := trimSuffixes(e.Name(), ".bak", ".json")
		return id, id != "" && !live[id]
	})
	assets := orphansIn(assetsRoot, func(e os.DirEntry) (string, bool) {
		return e.Name(), e.IsDir() && !live[e.Name()]
	})

	// A store with no profiles at all but files on disk is the shape the
	// corrupt-index bug produced, and it is not a shape ordinary use reaches:
	// RemoveProfile takes a profile's layout and assets with it, so the last
	// profile leaving is not supposed to strand anything. Refuse rather than
	// guess.
	if len(live) == 0 && len(layouts)+len(assets) > 0 {
		s.log.Warn("not pruning orphans: no profiles are loaded, but the store "+
			"still holds files, which does not happen in normal use",
			"layouts", len(layouts), "assets", len(assets))
		return nil
	}

	if len(layouts)+len(assets) == 0 {
		return nil
	}

	dest, err := s.newQuarantineDir()
	if err != nil {
		return err
	}

	for _, name := range layouts {
		s.quarantine(filepath.Join(profilesDir, name), filepath.Join(dest, profilesDirName), name, "layout")
	}
	for _, name := range assets {
		s.quarantine(filepath.Join(assetsRoot, name), filepath.Join(dest, assetsDirName), name, "assets")
	}

	s.log.Info("moved orphaned files to quarantine",
		"path", dest, "layouts", len(layouts), "assets", len(assets))
	return nil
}

// orphansIn lists the entries of a directory that match, by name.
//
// The whole listing is collected before anything is moved so the two guards
// above can weigh what a prune would actually do before it does it.
func orphansIn(dir string, match func(os.DirEntry) (string, bool)) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A directory that is not there holds no orphans. Nothing else about
		// it is this function's business.
		return nil
	}

	var out []string
	for _, e := range entries {
		if _, ok := match(e); ok {
			out = append(out, e.Name())
		}
	}
	return out
}

// newQuarantineDir makes a timestamped directory for one prune's leftovers, so
// successive prunes cannot overwrite each other's rescued files.
func (s *Store) newQuarantineDir() (string, error) {
	dir := filepath.Join(s.root, quarantineDirName, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create quarantine directory %s: %w", dir, err)
	}
	return dir, nil
}

// quarantine moves one orphan aside. Failing to move it is not fatal: the file
// is unreferenced either way, and a locked file should not stop startup.
func (s *Store) quarantine(from, destDir, name, kind string) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		s.log.Warn("could not prepare quarantine directory", "path", destDir, "error", err)
		return
	}
	if err := os.Rename(from, filepath.Join(destDir, name)); err != nil {
		s.log.Warn("could not quarantine an orphan", "kind", kind, "path", from, "error", err)
	}
}

// trimSuffixes strips each suffix from name, in order, when present.
func trimSuffixes(name string, suffixes ...string) string {
	for _, suffix := range suffixes {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}
