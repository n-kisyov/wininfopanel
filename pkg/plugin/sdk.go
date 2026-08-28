package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Plugin is what a plugin author implements.
//
// Only Info and Load are required to do anything useful; the rest have no-op
// defaults available by embedding Base.
type Plugin interface {
	// Info describes the plugin and how often it wants updating.
	Info() Info

	// Initialize runs once before Load, for setup that can fail.
	Initialize(ctx context.Context) error

	// Load declares the containers and entries the plugin publishes.
	//
	// It is called once, after Initialize. Entries returned here are the ones
	// the user can bind display items to, so their IDs must be stable across
	// runs: a saved layout addresses them by ID.
	Load(ctx context.Context) ([]*ContainerBuilder, error)

	// Update refreshes values. It is called on the interval Info requests.
	Update(ctx context.Context) error

	// Close releases resources on shutdown.
	Close() error
}

// Info describes a plugin.
type Info struct {
	// ID must be stable and unique; saved layouts address sensors by it.
	ID          string
	Name        string
	Description string
	Version     string
	Author      string
	Website     string

	// UpdateInterval is how often Update is called. Zero means one second.
	UpdateInterval time.Duration
}

// Actionable is implemented by plugins exposing buttons in the UI.
type Actionable interface {
	// Actions lists the available actions.
	Actions() []ActionInfo
	// Invoke runs one by name.
	Invoke(ctx context.Context, name string) error
}

// Configurable is implemented by plugins with editable settings.
//
// The host persists the values and replays them through Apply on the next
// start, so a plugin never has to write a config file of its own.
type Configurable interface {
	// Config returns the current properties.
	Config() []ConfigProperty
	// Apply sets one property.
	Apply(key string, value any) error
}

// Base provides no-op implementations of the optional Plugin methods, so a
// plugin only has to write the ones it cares about.
type Base struct{}

// Initialize implements Plugin.
func (Base) Initialize(context.Context) error { return nil }

// Update implements Plugin.
func (Base) Update(context.Context) error { return nil }

// Close implements Plugin.
func (Base) Close() error { return nil }

// ContainerBuilder collects the entries in one container.
type ContainerBuilder struct {
	id   string
	name string

	mu      sync.Mutex
	order   []string
	sensors map[string]*Sensor
	texts   map[string]*Text
	tables  map[string]*TableValue
}

// NewContainer returns a container to add entries to.
func NewContainer(id, name string) *ContainerBuilder {
	return &ContainerBuilder{
		id:      id,
		name:    name,
		sensors: make(map[string]*Sensor),
		texts:   make(map[string]*Text),
		tables:  make(map[string]*TableValue),
	}
}

// ID returns the container's identifier.
func (c *ContainerBuilder) ID() string { return c.id }

// AddSensor registers a numeric reading and returns the handle to update it.
func (c *ContainerBuilder) AddSensor(id, name, unit string) *Sensor {
	c.mu.Lock()
	defer c.mu.Unlock()

	sensor := &Sensor{id: id, name: name, unit: unit, window: make([]float64, 0, avgWindow)}
	c.sensors[id] = sensor
	c.order = append(c.order, id)
	return sensor
}

// AddText registers a string value.
func (c *ContainerBuilder) AddText(id, name, initial string) *Text {
	c.mu.Lock()
	defer c.mu.Unlock()

	text := &Text{id: id, name: name, value: initial}
	c.texts[id] = text
	c.order = append(c.order, id)
	return text
}

// AddTable registers tabular data.
func (c *ContainerBuilder) AddTable(id, name, format string, columns []string) *TableValue {
	c.mu.Lock()
	defer c.mu.Unlock()

	table := &TableValue{id: id, name: name, format: format, columns: columns}
	c.tables[id] = table
	c.order = append(c.order, id)
	return table
}

// snapshot renders the container's current state for the wire.
func (c *ContainerBuilder) snapshot() Container {
	c.mu.Lock()
	defer c.mu.Unlock()

	entries := make([]Entry, 0, len(c.order))
	for _, id := range c.order {
		switch {
		case c.sensors[id] != nil:
			entries = append(entries, c.sensors[id].entry())
		case c.texts[id] != nil:
			entries = append(entries, c.texts[id].entry())
		case c.tables[id] != nil:
			entries = append(entries, c.tables[id].entry())
		}
	}
	return Container{ID: c.id, Name: c.name, Entries: entries}
}

// changed collects the entries whose value moved since the last collection.
func (c *ContainerBuilder) changed() []EntryUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()

	var updates []EntryUpdate
	for _, id := range c.order {
		path := c.id + "/" + id

		switch {
		case c.sensors[id] != nil:
			if update, ok := c.sensors[id].takeChange(path); ok {
				updates = append(updates, update)
			}
		case c.texts[id] != nil:
			if update, ok := c.texts[id].takeChange(path); ok {
				updates = append(updates, update)
			}
		case c.tables[id] != nil:
			if update, ok := c.tables[id].takeChange(path); ok {
				updates = append(updates, update)
			}
		}
	}
	return updates
}

// avgWindow is how many samples the rolling average covers, matching
// InfoPanel's own sensor behaviour.
const avgWindow = 60

// Sensor is a numeric reading a plugin publishes.
//
// Minimum, maximum, and a rolling average are tracked automatically, so a
// plugin only ever sets the current value.
type Sensor struct {
	id   string
	name string
	unit string

	mu     sync.Mutex
	value  float64
	min    float64
	max    float64
	window []float64
	next   int

	started bool
	dirty   bool
}

// Set records a new reading.
func (s *Sensor) Set(value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started && s.value == value {
		return // unchanged; nothing to send
	}

	s.value = value
	// The first sample seeds both extremes: starting from zero would report a
	// false minimum for any sensor that never reaches it.
	if !s.started {
		s.min, s.max = value, value
		s.started = true
	} else {
		if value < s.min {
			s.min = value
		}
		if value > s.max {
			s.max = value
		}
	}

	if len(s.window) < avgWindow {
		s.window = append(s.window, value)
	} else {
		s.window[s.next] = value
		s.next = (s.next + 1) % avgWindow
	}

	s.dirty = true
}

// Value returns the current reading.
func (s *Sensor) Value() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

// Reset clears the accumulated extremes and average.
func (s *Sensor) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.min, s.max = s.value, s.value
	s.window = s.window[:0]
	s.next = 0
	s.dirty = true
}

func (s *Sensor) avg() float64 {
	if len(s.window) == 0 {
		return 0
	}
	var total float64
	for _, v := range s.window {
		total += v
	}
	return total / float64(len(s.window))
}

func (s *Sensor) entry() Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	return Entry{
		ID: s.id, Name: s.name, Type: EntrySensor, Unit: s.unit,
		Value: s.value, Min: s.min, Max: s.max, Avg: s.avg(),
	}
}

// takeChange returns an update if the value moved, clearing the flag.
func (s *Sensor) takeChange(path string) (EntryUpdate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return EntryUpdate{}, false
	}
	s.dirty = false

	return EntryUpdate{
		Path: path, Value: s.value, Min: s.min, Max: s.max, Avg: s.avg(),
	}, true
}

// Text is a string value a plugin publishes.
type Text struct {
	id   string
	name string

	mu    sync.Mutex
	value string
	dirty bool
}

// Set records a new value.
func (t *Text) Set(value string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.value == value {
		return
	}
	t.value = value
	t.dirty = true
}

// Value returns the current value.
func (t *Text) Value() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.value
}

func (t *Text) entry() Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Entry{ID: t.id, Name: t.name, Type: EntryText, Text: t.value}
}

func (t *Text) takeChange(path string) (EntryUpdate, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.dirty {
		return EntryUpdate{}, false
	}
	t.dirty = false
	return EntryUpdate{Path: path, Text: t.value}, true
}

// TableValue is tabular data a plugin publishes.
type TableValue struct {
	id      string
	name    string
	format  string
	columns []string

	mu    sync.Mutex
	rows  [][]string
	dirty bool
}

// SetRows replaces the table's contents.
func (t *TableValue) SetRows(rows [][]string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if sameRows(t.rows, rows) {
		return
	}
	t.rows = rows
	t.dirty = true
}

// sameRows reports whether two tables hold identical text, so an unchanged
// table is not resent every cycle.
func sameRows(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func (t *TableValue) table() *Table {
	return &Table{Columns: t.columns, Rows: t.rows, Format: t.format}
}

func (t *TableValue) entry() Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Entry{ID: t.id, Name: t.name, Type: EntryTable, Table: t.table()}
}

func (t *TableValue) takeChange(path string) (EntryUpdate, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.dirty {
		return EntryUpdate{}, false
	}
	t.dirty = false
	return EntryUpdate{Path: path, Table: t.table()}, true
}

// validateInfo rejects metadata that would make a plugin unusable.
//
// An empty ID is the important one: saved layouts address sensors by it, so a
// plugin without one produces bindings that can never resolve.
func validateInfo(info Info) error {
	if info.ID == "" {
		return fmt.Errorf("plugin ID is required; saved layouts address sensors by it")
	}
	if info.Name == "" {
		return fmt.Errorf("plugin name is required")
	}
	return nil
}
