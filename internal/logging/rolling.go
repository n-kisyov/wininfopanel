package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// rollingWriter writes to a dated log file, starting a new file when the day
// changes and pruning old files by age and by total size on disk.
//
// InfoPanel keeps 7 days of logs capped at 100MB; we match that.
type rollingWriter struct {
	dir      string
	prefix   string
	maxAge   time.Duration
	maxTotal int64
	now      func() time.Time // injectable for tests

	mu   sync.Mutex
	file *os.File
	day  string // the date stamp of the currently open file
}

func newRollingWriter(dir, prefix string, maxAge time.Duration, maxTotal int64) *rollingWriter {
	return &rollingWriter{
		dir:      dir,
		prefix:   prefix,
		maxAge:   maxAge,
		maxTotal: maxTotal,
		now:      time.Now,
	}
}

func (w *rollingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	day := w.now().Format("20060102")
	if w.file == nil || w.day != day {
		if err := w.rotate(day); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

// rotate closes the current file, opens the one for day, and prunes.
// The caller must hold w.mu.
func (w *rollingWriter) rotate(day string) error {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}

	name := filepath.Join(w.dir, fmt.Sprintf("%s-%s.log", w.prefix, day))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", name, err)
	}
	w.file = f
	w.day = day

	// Pruning is best effort: losing a stale log is never worth failing a
	// write that the caller is depending on.
	w.prune()
	return nil
}

// prune removes log files older than maxAge, then removes the oldest
// remaining files until the total size fits within maxTotal.
// The caller must hold w.mu.
func (w *rollingWriter) prune() {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}

	type logFile struct {
		path string
		mod  time.Time
		size int64
	}

	var files []logFile
	cutoff := w.now().Add(-w.maxAge)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), w.prefix+"-") || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(w.dir, e.Name())
		if path == w.file.Name() {
			continue // never prune the file we are writing to
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(path)
			continue
		}
		files = append(files, logFile{path: path, mod: info.ModTime(), size: info.Size()})
	}

	var total int64
	for _, f := range files {
		total += f.size
	}
	if total <= w.maxTotal {
		return
	}

	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= w.maxTotal {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

func (w *rollingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
