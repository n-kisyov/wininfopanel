package native

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

func TestSeriesTracksExtremesFromFirstSample(t *testing.T) {
	// Seeding min from zero would report a spurious minimum for any sensor
	// that never reaches it, e.g. an idle clock speed.
	s := newSeries()
	s.add(50)

	if s.min != 50 || s.max != 50 {
		t.Errorf("after one sample min/max = %v/%v, want 50/50", s.min, s.max)
	}

	s.add(30)
	s.add(80)
	if s.min != 30 {
		t.Errorf("min = %v, want 30", s.min)
	}
	if s.max != 80 {
		t.Errorf("max = %v, want 80", s.max)
	}
	if s.value != 80 {
		t.Errorf("value = %v, want the most recent sample 80", s.value)
	}
}

func TestSeriesAverageOverPartialWindow(t *testing.T) {
	s := newSeries()
	for _, v := range []float64{10, 20, 30} {
		s.add(v)
	}

	if got := s.avg(); got != 20 {
		t.Errorf("avg = %v, want 20 over the three samples seen so far", got)
	}
}

func TestSeriesAverageRollsOverWindow(t *testing.T) {
	s := newSeries()
	// Fill the window with zeros, then push a full window of hundreds; only
	// the recent samples should count.
	for i := 0; i < defaultWindow; i++ {
		s.add(0)
	}
	for i := 0; i < defaultWindow; i++ {
		s.add(100)
	}

	if got := s.avg(); got != 100 {
		t.Errorf("avg = %v, want 100 once the window has rolled over", got)
	}
}

func TestSeriesResetClearsExtremes(t *testing.T) {
	s := newSeries()
	s.add(10)
	s.add(90)
	s.add(50)

	s.reset()

	if s.min != 50 || s.max != 50 {
		t.Errorf("after reset min/max = %v/%v, want both at the current value 50", s.min, s.max)
	}
	if got := s.avg(); got != 0 {
		t.Errorf("after reset avg = %v, want 0 with an empty window", got)
	}
}

func TestSanitizePathSegment(t *testing.T) {
	// Device names come from the OS and routinely contain separators that
	// would make a sensor path ambiguous.
	tests := []struct{ in, want string }{
		{"C:", "C_"},
		{`C:\`, "C__"},
		{"vEthernet (vSwitch01)", "vEthernet__vSwitch01_"},
		{"Ethernet", "Ethernet"},
		{"disk/0", "disk_0"},
		{"my-device_1.0", "my-device_1.0"},
	}
	for _, tt := range tests {
		if got := sanitizePathSegment(tt.in); got != tt.want {
			t.Errorf("sanitizePathSegment(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPerSecondKBHandlesCounterReset(t *testing.T) {
	// Disabling and re-enabling an adapter resets its counters; a decrease
	// must not surface as a large negative rate.
	if got := perSecondKB(100, 5000, 1); got != 0 {
		t.Errorf("perSecondKB with a reset counter = %v, want 0", got)
	}

	if got := perSecondKB(2048+1024, 1024, 2); got != 1 {
		t.Errorf("perSecondKB = %v, want 1 KB/s (2048 bytes over 2 seconds)", got)
	}
}

// stubCollector produces a fixed set of samples, or fails.
type stubCollector struct {
	label   string
	samples func(out *sampleSet)
	err     error
}

func (s *stubCollector) name() string { return s.label }

func (s *stubCollector) collect(_ context.Context, out *sampleSet) error {
	if s.samples != nil {
		s.samples(out)
	}
	return s.err
}

func TestSampleSetKeepsCollectorsIndependent(t *testing.T) {
	set := newSampleSet()

	set.beginCollector("cpu")
	set.add("cpu/load", "CPU", "Load", "Usage", "%", 50)

	set.beginCollector("memory")
	set.add("memory/load", "Memory", "Load", "Usage", "%", 60)

	all := set.all()
	if len(all) != 2 {
		t.Fatalf("got %d samples, want 2", len(all))
	}
}

func TestSampleSetAbandonKeepsPreviousGoodSamples(t *testing.T) {
	// A collector that fails partway must not publish a partial picture, and
	// must not wipe out what it reported last time.
	set := newSampleSet()

	set.beginCollector("cpu")
	set.add("cpu/load", "CPU", "Load", "Usage", "%", 50)
	set.commit()

	set.beginCollector("cpu")
	set.add("cpu/load", "CPU", "Load", "Usage", "%", 999)
	set.abandonCollector()

	all := set.all()
	if len(all) != 1 {
		t.Fatalf("got %d samples, want 1", len(all))
	}
	if all[0].value != 50 {
		t.Errorf("value = %v, want the last good sample 50", all[0].value)
	}
}

func TestMonitorPublishesSamplesAndAccumulatesStatistics(t *testing.T) {
	m := &Monitor{
		log:     testLogger(),
		opts:    Options{Interval: time.Second},
		entries: make(map[sensor.Key]Entry),
		stats:   make(map[sensor.Key]*series),
	}

	set := newSampleSet()
	set.beginCollector("cpu")
	set.add("cpu/load", "CPU", "Total CPU Usage", "Usage", "%", 40)
	m.publish(set)

	set.beginCollector("cpu")
	set.add("cpu/load", "CPU", "Total CPU Usage", "Usage", "%", 80)
	m.publish(set)

	reading, ok := m.Read(sensor.Key{Source: sensor.SourceNative, Path: "cpu/load"})
	if !ok {
		t.Fatal("cpu/load did not resolve")
	}
	if reading.Now != 80 {
		t.Errorf("Now = %v, want 80", reading.Now)
	}
	if reading.Min != 40 || reading.Max != 80 {
		t.Errorf("Min/Max = %v/%v, want 40/80 accumulated across polls", reading.Min, reading.Max)
	}
	if reading.Avg != 60 {
		t.Errorf("Avg = %v, want 60", reading.Avg)
	}
}

func TestMonitorPublishesTextSensorsWithoutStatistics(t *testing.T) {
	m := &Monitor{
		log:     testLogger(),
		entries: make(map[sensor.Key]Entry),
		stats:   make(map[sensor.Key]*series),
	}

	set := newSampleSet()
	set.beginCollector("system")
	set.addText("system/os", "System", "Operating System", "Windows 11")
	m.publish(set)

	reading, ok := m.Read(sensor.Key{Source: sensor.SourceNative, Path: "system/os"})
	if !ok {
		t.Fatal("system/os did not resolve")
	}
	if reading.Text != "Windows 11" {
		t.Errorf("Text = %q, want %q", reading.Text, "Windows 11")
	}
	if len(m.stats) != 0 {
		t.Error("a text sensor allocated a statistics accumulator")
	}
}

func TestMonitorRejectsForeignSources(t *testing.T) {
	m := New(Options{})
	if _, ok := m.Read(sensor.Key{Source: sensor.SourceHWiNFO, ID: 1}); ok {
		t.Error("the native monitor resolved an HWiNFO sensor key")
	}
}

func TestMonitorIsolatesFailingCollectors(t *testing.T) {
	m := &Monitor{
		log:     testLogger(),
		entries: make(map[sensor.Key]Entry),
		stats:   make(map[sensor.Key]*series),
		fast: []collector{
			&stubCollector{label: "good", samples: func(out *sampleSet) {
				out.add("good/value", "Good", "Value", "Usage", "%", 42)
			}},
			&stubCollector{label: "broken", err: errors.New("no such device")},
		},
	}

	set := newSampleSet()
	m.runCollectors(context.Background(), m.fast, set)
	m.publish(set)

	if _, ok := m.Read(sensor.Key{Source: sensor.SourceNative, Path: "good/value"}); !ok {
		t.Error("a failing collector suppressed a healthy one")
	}
}

func TestMonitorEntriesOrderIsStable(t *testing.T) {
	// The sensor tree must not reshuffle under the user between polls.
	m := &Monitor{
		log:     testLogger(),
		entries: make(map[sensor.Key]Entry),
		stats:   make(map[sensor.Key]*series),
	}

	populate := func(value float64) {
		set := newSampleSet()
		set.beginCollector("cpu")
		for _, path := range []string{"cpu/load", "cpu/0/load", "cpu/1/load", "cpu/clock"} {
			set.add(path, "CPU", path, "Usage", "%", value)
		}
		m.publish(set)
	}

	populate(1)
	first := m.Entries()
	for i := 0; i < 5; i++ {
		populate(float64(i))
	}
	second := m.Entries()

	if len(first) != len(second) {
		t.Fatalf("entry count changed from %d to %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Key != second[i].Key {
			t.Errorf("position %d changed from %v to %v", i, first[i].Key, second[i].Key)
		}
	}
}

func TestNewClampsIntervals(t *testing.T) {
	m := New(Options{Interval: time.Millisecond})
	if m.opts.Interval != DefaultInterval {
		t.Errorf("Interval = %v, want it raised to %v", m.opts.Interval, DefaultInterval)
	}
	if m.opts.StorageInterval != DefaultStorageInterval {
		t.Errorf("StorageInterval = %v, want the default %v", m.opts.StorageInterval, DefaultStorageInterval)
	}
}

func TestStorageCollectorIsOptional(t *testing.T) {
	without := New(Options{StorageEnabled: false})
	with := New(Options{StorageEnabled: true})

	if len(with.slow) != len(without.slow)+1 {
		t.Errorf("enabling storage added %d collectors, want 1",
			len(with.slow)-len(without.slow))
	}
}
