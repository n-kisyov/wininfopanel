package store

import (
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
)

// layoutNamed builds a one-item layout whose name identifies the state, so
// history assertions read clearly.
func layoutNamed(name string) model.ItemList {
	return model.ItemList{model.NewTextItem(name)}
}

func firstName(items model.ItemList) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].Base().Name
}

func TestUndoRestoresPreviousState(t *testing.T) {
	m := NewUndoManager(10)
	const profile = "p1"

	before := layoutNamed("first")
	if err := m.Snapshot(profile, before); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	current := layoutNamed("second")

	restored, ok, err := m.Undo(profile, current)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if !ok {
		t.Fatal("Undo reported nothing to undo")
	}
	if got := firstName(restored); got != "first" {
		t.Errorf("restored %q, want %q", got, "first")
	}
}

func TestRedoReappliesUndoneState(t *testing.T) {
	m := NewUndoManager(10)
	const profile = "p1"

	if err := m.Snapshot(profile, layoutNamed("first")); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	current := layoutNamed("second")

	restored, _, err := m.Undo(profile, current)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}

	redone, ok, err := m.Redo(profile, restored)
	if err != nil {
		t.Fatalf("Redo: %v", err)
	}
	if !ok {
		t.Fatal("Redo reported nothing to redo")
	}
	if got := firstName(redone); got != "second" {
		t.Errorf("redone %q, want %q", got, "second")
	}
}

func TestUndoOnEmptyHistoryReportsNothingToDo(t *testing.T) {
	m := NewUndoManager(10)

	_, ok, err := m.Undo("p1", layoutNamed("only"))
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if ok {
		t.Error("Undo reported a restore with no history")
	}
}

func TestSnapshotDiscardsRedoHistory(t *testing.T) {
	// After undoing and then making a fresh edit, the abandoned branch must
	// not remain reachable through redo.
	m := NewUndoManager(10)
	const profile = "p1"

	if err := m.Snapshot(profile, layoutNamed("first")); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restored, _, err := m.Undo(profile, layoutNamed("second"))
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if !m.CanRedo(profile) {
		t.Fatal("expected a redo to be available after undoing")
	}

	if err := m.Snapshot(profile, restored); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if m.CanRedo(profile) {
		t.Error("a new edit did not discard the redo history")
	}
}

func TestUndoDepthIsBounded(t *testing.T) {
	m := NewUndoManager(3)
	const profile = "p1"

	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if err := m.Snapshot(profile, layoutNamed(name)); err != nil {
			t.Fatalf("Snapshot(%s): %v", name, err)
		}
	}

	// Only the three most recent snapshots survive: c, d, e.
	current := layoutNamed("f")
	for _, want := range []string{"e", "d", "c"} {
		restored, ok, err := m.Undo(profile, current)
		if err != nil {
			t.Fatalf("Undo: %v", err)
		}
		if !ok {
			t.Fatalf("history exhausted before reaching %q", want)
		}
		if got := firstName(restored); got != want {
			t.Errorf("restored %q, want %q", got, want)
		}
		current = restored
	}

	if _, ok, _ := m.Undo(profile, current); ok {
		t.Error("history retained more than the configured depth")
	}
}

func TestHistoryIsPerProfile(t *testing.T) {
	m := NewUndoManager(10)

	if err := m.Snapshot("p1", layoutNamed("p1-first")); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if m.CanUndo("p2") {
		t.Error("a snapshot on one profile made another profile undoable")
	}

	restored, ok, err := m.Undo("p1", layoutNamed("p1-second"))
	if err != nil || !ok {
		t.Fatalf("Undo: %v (ok=%v)", err, ok)
	}
	if got := firstName(restored); got != "p1-first" {
		t.Errorf("restored %q, want %q", got, "p1-first")
	}
}

func TestClearDiscardsHistory(t *testing.T) {
	m := NewUndoManager(10)
	const profile = "p1"

	if err := m.Snapshot(profile, layoutNamed("first")); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	m.Clear(profile)

	if m.CanUndo(profile) {
		t.Error("history survived Clear")
	}
}

func TestSnapshotPreservesItemKinds(t *testing.T) {
	// History round-trips through JSON, so polymorphic items must come back as
	// their original types rather than as a base item.
	m := NewUndoManager(10)
	const profile = "p1"

	original := model.ItemList{
		model.NewGraphItem("History", model.GraphHistogram),
		model.NewGaugeItem("Fan"),
	}
	if err := m.Snapshot(profile, original); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored, ok, err := m.Undo(profile, nil)
	if err != nil || !ok {
		t.Fatalf("Undo: %v (ok=%v)", err, ok)
	}
	if len(restored) != 2 {
		t.Fatalf("restored %d items, want 2", len(restored))
	}
	if restored[0].Kind() != model.KindGraph || restored[1].Kind() != model.KindGauge {
		t.Errorf("kinds = %s, %s; want Graph, Gauge", restored[0].Kind(), restored[1].Kind())
	}
	if graph, ok := restored[0].(*model.GraphItem); !ok || graph.Type != model.GraphHistogram {
		t.Error("graph item lost its type through the history round trip")
	}
}
