package logging

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Entry is one captured log record, as surfaced on the Logs page.
type Entry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Source  string         `json:"source,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// MemorySink keeps the most recent log records in a fixed-size ring buffer so
// the UI can display them without reading back from disk.
//
// It is safe for concurrent use.
type MemorySink struct {
	mu      sync.RWMutex
	entries []Entry
	next    int
	full    bool
}

// NewMemorySink returns a sink retaining the last capacity records.
func NewMemorySink(capacity int) *MemorySink {
	if capacity <= 0 {
		capacity = 1000
	}
	return &MemorySink{entries: make([]Entry, capacity)}
}

func (s *MemorySink) append(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[s.next] = e
	s.next = (s.next + 1) % len(s.entries)
	if s.next == 0 {
		s.full = true
	}
}

// Entries returns the retained records in chronological order.
func (s *MemorySink) Entries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.full {
		out := make([]Entry, s.next)
		copy(out, s.entries[:s.next])
		return out
	}

	out := make([]Entry, 0, len(s.entries))
	out = append(out, s.entries[s.next:]...)
	out = append(out, s.entries[:s.next]...)
	return out
}

// Clear discards all retained records.
func (s *MemorySink) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next = 0
	s.full = false
}

// memoryHandler is the slog.Handler that feeds a MemorySink.
type memoryHandler struct {
	sink  *MemorySink
	level slog.Leveler
	attrs []slog.Attr
	group string
}

func (h *memoryHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *memoryHandler) Handle(_ context.Context, r slog.Record) error {
	e := Entry{
		Time:    r.Time,
		Level:   r.Level.String(),
		Message: r.Message,
	}

	collect := func(a slog.Attr) {
		if a.Key == sourceKey {
			e.Source = a.Value.String()
			return
		}
		if e.Attrs == nil {
			e.Attrs = make(map[string]any)
		}
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		e.Attrs[key] = a.Value.Any()
	}

	for _, a := range h.attrs {
		collect(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		collect(a)
		return true
	})

	h.sink.append(e)
	return nil
}

func (h *memoryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &memoryHandler{sink: h.sink, level: h.level, attrs: merged, group: h.group}
}

func (h *memoryHandler) WithGroup(name string) slog.Handler {
	group := name
	if h.group != "" && name != "" {
		group = h.group + "." + name
	}
	return &memoryHandler{sink: h.sink, level: h.level, attrs: h.attrs, group: group}
}
