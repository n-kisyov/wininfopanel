package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// writeJSONAtomic serializes v to path without ever leaving a truncated file
// behind: it writes a sibling temp file, fsyncs it, rotates the previous
// contents to path+".bak", then renames into place.
//
// The backup matters because a profile or settings file is the user's work; a
// power loss mid-write should cost at most the current save, never the layout.
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')

	// Encoding is per-caller work; everything past here touches the one path
	// and has to be the only writer doing so.
	defer lockPath(path)()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}

	// Keep the previous good copy. A missing original is the first save.
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, path+".bak"); err != nil {
			return fmt.Errorf("back up %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// pathLocks serialises writes, one mutex per file.
//
// writeJSONAtomic is crash-safe on its own: the temp file, the rotation of the
// old contents to ".bak", and the rename into place are ordered so an
// interrupted write costs at most the save in progress. It is not safe against
// a second writer. Two overlapping saves of the same file can rotate a
// half-written copy into the backup slot, which leaves the recovery path
// holding a second copy of the damage rather than the good previous version.
//
// The store saves from HTTP handler goroutines, so two concurrent requests
// touching the same profile reach exactly that.
var pathLocks sync.Map // canonical path -> *sync.Mutex

// lockPath takes the lock for one file and returns the release.
func lockPath(path string) func() {
	// Windows paths are case-insensitive, so two spellings of one file must
	// not end up with two different mutexes.
	key := strings.ToLower(filepath.Clean(path))

	value, _ := pathLocks.LoadOrStore(key, new(sync.Mutex))
	mu := value.(*sync.Mutex)

	mu.Lock()
	return mu.Unlock
}

// ErrRecoveredFromBackup reports that the primary file was unreadable and its
// ".bak" copy was used instead. It accompanies successfully decoded data, so
// callers must treat it as a warning to surface rather than a failure: an
// error that also carries this sentinel means v was populated.
var ErrRecoveredFromBackup = errors.New("recovered from backup")

// readJSON decodes path into v.
//
// If the primary file is missing or corrupt it falls back to the ".bak" copy
// written by writeJSONAtomic, which is the whole point of keeping one. The
// bool result reports whether anything was read at all; a missing file with no
// backup is not an error, it is a first run.
func readJSON(path string, v any) (bool, error) {
	err := readJSONFile(path, v)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		// Nothing at the primary path: a backup may still exist if the last
		// rename was interrupted.
		if backupErr := readJSONFile(path+".bak", v); backupErr == nil {
			return true, nil
		}
		return false, nil
	}

	// The primary file exists but did not parse. Prefer stale-but-valid data
	// over losing the user's configuration entirely.
	if backupErr := readJSONFile(path+".bak", v); backupErr == nil {
		return true, fmt.Errorf("%s was unreadable, so its backup was used instead: %w (%w)",
			filepath.Base(path), err, ErrRecoveredFromBackup)
	}
	return false, err
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return nil
}
