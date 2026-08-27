package draw

import (
	"math"
	"sync"
)

// Smoother eases chart values toward their target across frames.
//
// Bars and donuts driven straight from a sensor jump on every poll, which
// reads as flicker; InfoPanel interpolates instead, and this reproduces that.
// Values are keyed by display item ID, so two charts of the same sensor
// animate independently.
//
// It is safe for concurrent use.
type Smoother struct {
	// cycles is how many frames a value takes to converge on its target.
	cycles int

	mu     sync.Mutex
	values map[string]float64
}

// NewSmoother returns a smoother converging over the given number of frames.
// A non-positive count disables smoothing.
func NewSmoother(cycles int) *Smoother {
	return &Smoother{cycles: cycles, values: make(map[string]float64)}
}

// CyclesForFrameRate returns the convergence window InfoPanel uses: three
// seconds' worth of frames at the configured rate.
func CyclesForFrameRate(frameRate int) int {
	if frameRate <= 0 {
		return 0
	}
	return frameRate * 3
}

// Step advances one value toward its target and returns the eased result.
//
// The first observation of an item snaps to the target rather than easing up
// from zero, so a panel does not visibly fill on startup.
func (s *Smoother) Step(id string, target float64) float64 {
	if s == nil || s.cycles <= 0 {
		return target
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, seen := s.values[id]
	if !seen {
		s.values[id] = target
		return target
	}

	next := interpolate(current, target, s.cycles)
	s.values[id] = next
	return next
}

// Forget drops an item's eased value, so a re-added item starts fresh.
func (s *Smoother) Forget(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, id)
}

// interpolate moves from toward to by the fraction that would close the gap to
// within tolerance after the given number of cycles.
//
// The step size is derived from the remaining gap on every call rather than
// being a fixed fraction, so the approach is asymptotic: it targets an
// absolute tolerance instead of a proportional one. A bar sweeping from 0 to
// 100 and one nudging from 50 to 51 therefore both look settled after roughly
// the same number of frames, which is what keeps the animation feeling
// consistent across very different jumps.
func interpolate(from, to float64, cycles int) float64 {
	if cycles <= 0 {
		return to
	}

	const tolerance = 0.001
	gap := math.Abs(to - from)
	if gap <= tolerance {
		return to
	}

	decay := math.Pow(tolerance/gap, 1.0/float64(cycles))
	t := math.Max(0, math.Min(1, 1-decay))
	return from + (to-from)*t
}
