// Package registry presents the three sensor sources -- HWiNFO shared memory,
// the native hardware monitor, and plugins -- behind one lookup.
//
// Display items resolve against a Registry and never learn which subsystem
// answered, which is what lets a profile bind to a plugin sensor and an HWiNFO
// sensor with the same code path.
package registry

import (
	"sync"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// Registry routes reads to whichever source owns the key.
//
// It is safe for concurrent use. Sources come and go at runtime -- HWiNFO may
// start after the app, a plugin host may crash -- so registration is dynamic
// rather than fixed at construction.
type Registry struct {
	mu        sync.RWMutex
	resolvers map[sensor.Source]sensor.Resolver
}

// New returns an empty registry that resolves nothing.
func New() *Registry {
	return &Registry{resolvers: make(map[sensor.Source]sensor.Resolver)}
}

// Register attaches a resolver for one source, replacing any previous one.
func (r *Registry) Register(source sensor.Source, resolver sensor.Resolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolvers[source] = resolver
}

// Unregister detaches a source, after which its sensors stop resolving.
//
// Display items bound to that source then render as unavailable rather than
// holding a stale value, which is the honest result when a plugin dies.
func (r *Registry) Unregister(source sensor.Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.resolvers, source)
}

// Sources lists the currently registered sources.
func (r *Registry) Sources() []sensor.Source {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]sensor.Source, 0, len(r.resolvers))
	for source := range r.resolvers {
		out = append(out, source)
	}
	return out
}

// Has reports whether a source is registered.
func (r *Registry) Has(source sensor.Source) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.resolvers[source]
	return ok
}

// Read implements sensor.Resolver.
func (r *Registry) Read(key sensor.Key) (sensor.Reading, bool) {
	r.mu.RLock()
	resolver, ok := r.resolvers[key.Source]
	r.mu.RUnlock()

	if !ok {
		return sensor.Reading{}, false
	}
	return resolver.Read(key)
}
