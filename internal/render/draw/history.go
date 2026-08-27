package draw

import (
	"sync"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// HistoryStore records the recent value of every graphed sensor.
//
// Graphs plot history, but a display item holds only its current binding, so
// the samples have to live somewhere that outlives a frame. Keying by sensor
// rather than by item means several graphs of the same sensor share one series
// and stay in step.
//
// It is safe for concurrent use: the sampler and the render loop both touch it.
type HistoryStore struct {
	// interval is the minimum spacing between recorded samples.
	interval time.Duration
	// capacity bounds each series.
	capacity int

	mu     sync.RWMutex
	series map[sensor.Key]*ring
}

// DefaultHistoryCapacity is generous enough for the widest graph at the
// smallest step, so a graph never runs out of history to draw.
const DefaultHistoryCapacity = 512

// NewHistoryStore returns a store sampling no faster than interval.
func NewHistoryStore(interval time.Duration, capacity int) *HistoryStore {
	if interval <= 0 {
		interval = time.Second
	}
	if capacity <= 0 {
		capacity = DefaultHistoryCapacity
	}
	return &HistoryStore{
		interval: interval,
		capacity: capacity,
		series:   make(map[sensor.Key]*ring),
	}
}

// Sample records the current value of a sensor, if enough time has passed
// since its last sample.
//
// Rate limiting lives here rather than at the call site so the render loop can
// sample on every frame and still produce a graph whose horizontal axis is
// time, not frame count.
func (h *HistoryStore) Sample(key sensor.Key, value float64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.series[key]
	if !ok {
		s = newRing(h.capacity)
		h.series[key] = s
	}
	if !s.last.IsZero() && now.Sub(s.last) < h.interval {
		return
	}

	s.push(value)
	s.last = now
}

// Values returns up to count of the most recent samples for a sensor, oldest
// first. The second result is false when the sensor has no history.
func (h *HistoryStore) Values(key sensor.Key, count int) ([]float64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	s, ok := h.series[key]
	if !ok || s.len == 0 {
		return nil, false
	}
	return s.tail(count), true
}

// Forget drops a sensor's history, for a binding no longer on screen.
func (h *HistoryStore) Forget(key sensor.Key) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.series, key)
}

// Retain drops the history of every sensor not in keep, so removing a graph
// does not leak its series forever.
func (h *HistoryStore) Retain(keep map[sensor.Key]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for key := range h.series {
		if !keep[key] {
			delete(h.series, key)
		}
	}
}

// ring is a fixed-capacity circular buffer of samples.
type ring struct {
	values []float64
	next   int
	len    int
	last   time.Time
}

func newRing(capacity int) *ring {
	return &ring{values: make([]float64, capacity)}
}

func (r *ring) push(v float64) {
	r.values[r.next] = v
	r.next = (r.next + 1) % len(r.values)
	if r.len < len(r.values) {
		r.len++
	}
}

// tail returns the most recent count samples, oldest first.
func (r *ring) tail(count int) []float64 {
	if count <= 0 || count > r.len {
		count = r.len
	}

	out := make([]float64, count)
	// Walk backwards from the write cursor so the newest sample lands last.
	for i := 0; i < count; i++ {
		index := (r.next - count + i + len(r.values)*2) % len(r.values)
		out[i] = r.values[index]
	}
	return out
}
