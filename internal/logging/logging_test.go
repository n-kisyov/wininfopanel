package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemorySinkRetainsMostRecentEntries(t *testing.T) {
	sink := NewMemorySink(3)
	for i, msg := range []string{"a", "b", "c", "d", "e"} {
		sink.append(Entry{Message: msg, Level: "INFO", Time: time.Unix(int64(i), 0)})
	}

	got := sink.Entries()
	if len(got) != 3 {
		t.Fatalf("Entries() returned %d entries, want 3", len(got))
	}
	for i, want := range []string{"c", "d", "e"} {
		if got[i].Message != want {
			t.Errorf("entry %d = %q, want %q", i, got[i].Message, want)
		}
	}
}

func TestMemorySinkBeforeWrapReturnsChronologicalPrefix(t *testing.T) {
	sink := NewMemorySink(5)
	sink.append(Entry{Message: "first"})
	sink.append(Entry{Message: "second"})

	got := sink.Entries()
	if len(got) != 2 || got[0].Message != "first" || got[1].Message != "second" {
		t.Fatalf("Entries() = %+v, want [first second]", got)
	}
}

func TestMemoryHandlerCapturesSourceAndAttrs(t *testing.T) {
	sink := NewMemorySink(10)
	log := slog.New(&memoryHandler{sink: sink, level: slog.LevelInfo})

	log.With(sourceKey, "hwinfo").Info("polled", "sensors", 42)

	entries := sink.Entries()
	if len(entries) != 1 {
		t.Fatalf("captured %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Source != "hwinfo" {
		t.Errorf("Source = %q, want %q", e.Source, "hwinfo")
	}
	if e.Message != "polled" {
		t.Errorf("Message = %q, want %q", e.Message, "polled")
	}
	if got := e.Attrs["sensors"]; got != int64(42) {
		t.Errorf("Attrs[sensors] = %v (%T), want 42", got, got)
	}
}

func TestMemoryHandlerRespectsLevel(t *testing.T) {
	sink := NewMemorySink(10)
	log := slog.New(&memoryHandler{sink: sink, level: slog.LevelWarn})

	log.Info("dropped")
	log.Warn("kept")

	entries := sink.Entries()
	if len(entries) != 1 || entries[0].Message != "kept" {
		t.Fatalf("Entries() = %+v, want only the warning", entries)
	}
}

func TestRollingWriterStartsNewFileWhenDayChanges(t *testing.T) {
	dir := t.TempDir()
	w := newRollingWriter(dir, "test", logMaxAge, logMaxTotal)
	defer w.Close()

	day := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return day }
	if _, err := w.Write([]byte("first\n")); err != nil {
		t.Fatalf("Write on day one: %v", err)
	}

	w.now = func() time.Time { return day.AddDate(0, 0, 1) }
	if _, err := w.Write([]byte("second\n")); err != nil {
		t.Fatalf("Write on day two: %v", err)
	}

	for _, name := range []string{"test-20260827.log", "test-20260828.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected log file %s: %v", name, err)
		}
	}
}

func TestRollingWriterPrunesFilesPastMaxAge(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "test-20200101.log")
	if err := os.WriteFile(stale, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	w := newRollingWriter(dir, "test", logMaxAge, logMaxTotal)
	defer w.Close()
	if _, err := w.Write([]byte("new\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale log was not pruned (err = %v)", err)
	}
}

func TestSetupWritesToFileAndMemory(t *testing.T) {
	dir := t.TempDir()
	lg, err := Setup(Options{Dir: dir, Prefix: "unit", Level: slog.LevelDebug})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer lg.Close()

	For("test").Info("hello", "n", 1)

	if entries := lg.Memory.Entries(); len(entries) != 1 || entries[0].Source != "test" {
		t.Errorf("memory sink = %+v, want one entry sourced to \"test\"", entries)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "unit-*.log"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one log file, got %v (err %v)", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("log file is empty; the record did not reach disk")
	}
}
