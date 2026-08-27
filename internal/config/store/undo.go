package store

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
)

// DefaultUndoDepth is how many edits are retained per profile.
const DefaultUndoDepth = 50

// UndoManager records layout history so design-canvas edits can be reversed.
//
// History is kept per profile, as serialized snapshots rather than as diffs.
// Snapshots are simpler to reason about and a layout is small; InfoPanel takes
// the same approach with XML.
type UndoManager struct {
	depth int

	mu     sync.Mutex
	stacks map[string]*undoStack
}

type undoStack struct {
	undo [][]byte
	redo [][]byte
}

// NewUndoManager returns a manager retaining depth edits per profile.
func NewUndoManager(depth int) *UndoManager {
	if depth <= 0 {
		depth = DefaultUndoDepth
	}
	return &UndoManager{depth: depth, stacks: make(map[string]*undoStack)}
}

// stackFor returns the profile's history, creating it on first use.
// The caller must hold m.mu.
func (m *UndoManager) stackFor(profileID string) *undoStack {
	s, ok := m.stacks[profileID]
	if !ok {
		s = &undoStack{}
		m.stacks[profileID] = s
	}
	return s
}

// Snapshot records the state of a layout before it is modified.
//
// Call it with the state as it exists *before* applying an edit. Recording a
// new snapshot discards the redo history, which is what makes redo mean "the
// branch I just undid" rather than an arbitrary older state.
func (m *UndoManager) Snapshot(profileID string, items model.ItemList) error {
	encoded, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("snapshot layout for profile %s: %w", profileID, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	s := m.stackFor(profileID)
	s.undo = append(s.undo, encoded)
	if len(s.undo) > m.depth {
		s.undo = s.undo[len(s.undo)-m.depth:]
	}
	s.redo = nil
	return nil
}

// Undo restores the previous layout, given the current one to push onto the
// redo stack. The bool result is false when there is nothing to undo.
func (m *UndoManager) Undo(profileID string, current model.ItemList) (model.ItemList, bool, error) {
	return m.step(profileID, current, true)
}

// Redo re-applies the most recently undone layout, given the current one.
// The bool result is false when there is nothing to redo.
func (m *UndoManager) Redo(profileID string, current model.ItemList) (model.ItemList, bool, error) {
	return m.step(profileID, current, false)
}

// step moves one entry between the undo and redo stacks. Both directions are
// the same operation with the stacks swapped.
func (m *UndoManager) step(profileID string, current model.ItemList, undoing bool) (model.ItemList, bool, error) {
	encodedCurrent, err := json.Marshal(current)
	if err != nil {
		return nil, false, fmt.Errorf("capture current layout for profile %s: %w", profileID, err)
	}

	m.mu.Lock()
	s := m.stackFor(profileID)

	from, to := &s.undo, &s.redo
	if !undoing {
		from, to = &s.redo, &s.undo
	}
	if len(*from) == 0 {
		m.mu.Unlock()
		return nil, false, nil
	}

	target := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, encodedCurrent)
	m.mu.Unlock()

	var items model.ItemList
	if err := json.Unmarshal(target, &items); err != nil {
		return nil, false, fmt.Errorf("restore layout for profile %s: %w", profileID, err)
	}
	return items, true, nil
}

// CanUndo reports whether an undo is available.
func (m *UndoManager) CanUndo(profileID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stacks[profileID]
	return ok && len(s.undo) > 0
}

// CanRedo reports whether a redo is available.
func (m *UndoManager) CanRedo(profileID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stacks[profileID]
	return ok && len(s.redo) > 0
}

// Clear discards a profile's history, for example after it is reloaded from
// disk and the in-memory history no longer describes it.
func (m *UndoManager) Clear(profileID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.stacks, profileID)
}
