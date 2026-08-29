// Package native is wininfopanel's built-in hardware monitor, the replacement
// for LibreHardwareMonitor.
//
// Coverage is deliberately phased. Phase A -- what exists today -- reads what
// Windows exposes without a driver: CPU and memory load, disk capacity and
// throughput, network rates, and system information. Later phases add GPU
// telemetry through nvml.dll and atiadlxx.dll, then CPU temperatures and
// voltages through the PawnIO driver, then SMART and motherboard sensors.
//
// Anything a phase cannot read is simply absent from the sensor list rather
// than reported as zero: a missing sensor is honest, a zero is a lie.
package native

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

const (
	// DefaultInterval is the poll rate for fast-changing sensors.
	DefaultInterval = time.Second
	// MinInterval is the floor accepted by SetInterval.
	MinInterval = 250 * time.Millisecond

	// DefaultStorageInterval paces disk capacity polling, which is far slower
	// than reading a counter and barely changes between polls.
	DefaultStorageInterval = 30 * time.Second
)

// Entry is one sensor this monitor publishes.
type Entry struct {
	Key sensor.Key `json:"key"`

	// Type classifies the reading, e.g. "Usage" or "Temperature", matching
	// the vocabulary HWiNFO uses so the two sensor trees read alike.
	Type string `json:"type"`
	Name string `json:"name"`
	Unit string `json:"unit"`
	// GroupName is the tree section this sensor belongs under, e.g. "CPU".
	GroupName string `json:"groupName"`

	Value float64 `json:"value"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Avg   float64 `json:"avg"`

	// Text carries string-valued sensors, such as an OS name, for which the
	// numeric fields are meaningless.
	Text string `json:"text,omitempty"`
}

// Options configures a Monitor.
type Options struct {
	// Interval paces the fast collectors. Defaults to DefaultInterval.
	Interval time.Duration
	// StorageEnabled includes disk capacity and throughput sensors.
	StorageEnabled bool
	// StorageInterval paces disk polling. Defaults to DefaultStorageInterval.
	StorageInterval time.Duration
}

// collector produces sensors for one subsystem.
//
// Collect appends to out rather than returning a slice so a single buffer is
// reused across polls; the monitor copies what it keeps.
type collector interface {
	// name identifies the collector in logs.
	name() string
	// collect gathers current readings. Returning an error skips this
	// collector for this cycle without affecting the others.
	collect(ctx context.Context, out *sampleSet) error
}

// Monitor polls the collectors and serves their readings.
//
// It is safe for concurrent use: Run owns the only writer goroutine.
type Monitor struct {
	log  *slog.Logger
	opts Options

	fast []collector
	slow []collector

	mu      sync.RWMutex
	entries map[sensor.Key]Entry
	order   []sensor.Key
	// stats accumulates min/max/average per sensor across polls, since the
	// underlying sources report only an instantaneous value.
	stats   map[sensor.Key]*series
	running bool
}

// New returns a monitor that is not yet polling. Call Run to start it.
func New(opts Options) *Monitor {
	if opts.Interval < MinInterval {
		opts.Interval = DefaultInterval
	}
	if opts.StorageInterval <= 0 {
		opts.StorageInterval = DefaultStorageInterval
	}

	m := &Monitor{
		log:     logging.For("sensor.native"),
		opts:    opts,
		entries: make(map[sensor.Key]Entry),
		stats:   make(map[sensor.Key]*series),
	}

	m.fast = []collector{
		newCPUCollector(),
		newMemoryCollector(),
		newNetworkCollector(),
		newGPUCollector(),
	}
	m.slow = []collector{
		newSystemCollector(),
	}
	if opts.StorageEnabled {
		m.slow = append(m.slow, newDiskCollector())
	}

	return m
}

// Available reports whether the monitor has produced readings.
func (m *Monitor) Available() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running && len(m.entries) > 0
}

// Read implements sensor.Resolver.
func (m *Monitor) Read(key sensor.Key) (sensor.Reading, bool) {
	if key.Source != sensor.SourceNative {
		return sensor.Reading{}, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.entries[key]
	if !ok {
		return sensor.Reading{}, false
	}
	return sensor.Reading{
		Name: entry.Name,
		Unit: entry.Unit,
		Now:  entry.Value,
		Min:  entry.Min,
		Max:  entry.Max,
		Avg:  entry.Avg,
		Text: entry.Text,
	}, true
}

// Entries returns every known sensor, in a stable order so the sensor tree
// does not reshuffle under the user while they are picking one.
func (m *Monitor) Entries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Entry, 0, len(m.order))
	for _, key := range m.order {
		if entry, ok := m.entries[key]; ok {
			out = append(out, entry)
		}
	}
	return out
}

// ResetStatistics clears accumulated minimums, maximums, and averages, the
// equivalent of HWiNFO's "reset values".
func (m *Monitor) ResetStatistics() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.stats {
		s.reset()
	}
}

// Run polls until ctx is cancelled.
//
// Fast and slow collectors run on separate cadences: disk capacity barely
// changes between polls and enumerating volumes is comparatively expensive.
func (m *Monitor) Run(ctx context.Context) error {
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()

	samples := newSampleSet()

	// Prime both cadences so the first frame has data.
	m.runCollectors(ctx, m.fast, samples)
	m.runCollectors(ctx, m.slow, samples)
	m.publish(samples)

	fast := time.NewTicker(m.opts.Interval)
	defer fast.Stop()
	slow := time.NewTicker(m.opts.StorageInterval)
	defer slow.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-fast.C:
			m.runCollectors(ctx, m.fast, samples)
			m.publish(samples)
		case <-slow.C:
			m.runCollectors(ctx, m.slow, samples)
			m.publish(samples)
		}
	}
}

// runCollectors gathers from each collector, isolating failures so one
// unavailable subsystem does not blank the rest.
func (m *Monitor) runCollectors(ctx context.Context, collectors []collector, samples *sampleSet) {
	for _, c := range collectors {
		samples.beginCollector(c.name())
		if err := c.collect(ctx, samples); err != nil {
			samples.abandonCollector()
			m.log.Debug("collector failed", "collector", c.name(), "error", err)
		}
	}
}

// publish merges the current samples into the served entry table, updating the
// accumulated statistics for each.
func (m *Monitor) publish(samples *sampleSet) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range samples.all() {
		key := s.key

		if s.text != "" {
			// Text sensors carry no statistics to accumulate.
			if _, known := m.entries[key]; !known {
				m.order = append(m.order, key)
			}
			m.entries[key] = Entry{
				Key: key, Type: s.readingType, Name: s.name,
				GroupName: s.group, Unit: s.unit, Text: s.text,
			}
			continue
		}

		stat, ok := m.stats[key]
		if !ok {
			stat = newSeries()
			m.stats[key] = stat
			m.order = append(m.order, key)
		}
		stat.add(s.value)

		m.entries[key] = Entry{
			Key: key, Type: s.readingType, Name: s.name,
			GroupName: s.group, Unit: s.unit,
			Value: stat.value, Min: stat.min, Max: stat.max, Avg: stat.avg(),
		}
	}
}
