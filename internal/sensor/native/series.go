package native

// series tracks the statistics a sensor reading carries: the current value,
// the extremes seen since start, and a rolling average.
//
// Sources like gopsutil report only an instantaneous value, so unlike HWiNFO
// -- which maintains min/max/average itself -- the monitor has to accumulate
// them.
type series struct {
	value float64
	min   float64
	max   float64

	// window is a ring of recent samples backing the average. A rolling
	// average over a fixed window, rather than a cumulative mean, keeps the
	// figure responsive to how the machine is behaving now.
	window []float64
	next   int
	filled bool

	started bool
}

// defaultWindow matches the plugin SDK's 60-sample average, which at a
// one-second poll is the last minute.
const defaultWindow = 60

func newSeries() *series {
	return &series{window: make([]float64, defaultWindow)}
}

// add records a sample.
func (s *series) add(v float64) {
	s.value = v

	// The first sample seeds both extremes; starting from zero would report a
	// spurious minimum of 0 for any sensor that never reaches it.
	if !s.started {
		s.min, s.max = v, v
		s.started = true
	} else {
		if v < s.min {
			s.min = v
		}
		if v > s.max {
			s.max = v
		}
	}

	s.window[s.next] = v
	s.next = (s.next + 1) % len(s.window)
	if s.next == 0 {
		s.filled = true
	}
}

// avg returns the rolling average over the retained window.
func (s *series) avg() float64 {
	count := s.next
	if s.filled {
		count = len(s.window)
	}
	if count == 0 {
		return 0
	}

	var total float64
	for _, v := range s.window[:count] {
		total += v
	}
	return total / float64(count)
}

// reset clears the accumulated extremes and average, keeping the last value.
func (s *series) reset() {
	s.min, s.max = s.value, s.value
	s.next = 0
	s.filled = false
	s.started = s.value != 0 || s.started
	for i := range s.window {
		s.window[i] = 0
	}
}
