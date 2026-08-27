package native

import "github.com/n-kisyov/wininfopanel/internal/sensor"

// sample is one reading produced by a collector during a poll.
type sample struct {
	key         sensor.Key
	readingType string
	name        string
	group       string
	unit        string
	value       float64
	text        string
}

// sampleSet accumulates the readings from one poll cycle.
//
// Each collector's output is scoped so a mid-collection failure can be
// discarded wholesale: a collector that read three of its ten sensors before
// erroring would otherwise publish a partial, misleading picture.
//
// Samples from a collector replace that collector's previous samples and leave
// every other collector's alone, which is what lets the fast and slow cadences
// share one set.
type sampleSet struct {
	// byCollector holds the last good samples from each collector.
	byCollector map[string][]sample
	// order preserves first-seen collector order for stable output.
	order []string

	// pending accumulates the collector currently running.
	current string
	pending []sample
}

func newSampleSet() *sampleSet {
	return &sampleSet{byCollector: make(map[string][]sample)}
}

// beginCollector starts accumulating samples for a collector.
func (s *sampleSet) beginCollector(name string) {
	s.commit()
	s.current = name
	s.pending = nil
}

// abandonCollector discards the in-progress samples, keeping the collector's
// previous good set.
func (s *sampleSet) abandonCollector() {
	s.current = ""
	s.pending = nil
}

// commit stores the in-progress samples as the collector's current set.
func (s *sampleSet) commit() {
	if s.current == "" {
		return
	}
	if _, seen := s.byCollector[s.current]; !seen {
		s.order = append(s.order, s.current)
	}
	s.byCollector[s.current] = s.pending
	s.current = ""
	s.pending = nil
}

// add records a numeric reading.
func (s *sampleSet) add(path, group, name, readingType, unit string, value float64) {
	s.pending = append(s.pending, sample{
		key:         sensor.Key{Source: sensor.SourceNative, Path: path},
		readingType: readingType,
		name:        name,
		group:       group,
		unit:        unit,
		value:       value,
	})
}

// addText records a string-valued reading, such as an OS name.
func (s *sampleSet) addText(path, group, name, text string) {
	s.pending = append(s.pending, sample{
		key:         sensor.Key{Source: sensor.SourceNative, Path: path},
		readingType: "Text",
		name:        name,
		group:       group,
		text:        text,
	})
}

// all returns every collector's current samples, in collector order.
func (s *sampleSet) all() []sample {
	s.commit()

	var out []sample
	for _, name := range s.order {
		out = append(out, s.byCollector[name]...)
	}
	return out
}
