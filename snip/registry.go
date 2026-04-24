package snip

import (
	"sort"
	"sync"
)

// Registry holds all registered SnipPlugin implementations, keyed by their
// Type() string. It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]SnipPlugin
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]SnipPlugin),
	}
}

// Register adds p to the registry. It panics if a plugin with the same Type()
// has already been registered, preventing accidental double-registration at
// startup.
func (r *Registry) Register(p SnipPlugin) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t := p.Type()
	if _, exists := r.plugins[t]; exists {
		panic("snip: plugin already registered for type \"" + t + "\"")
	}
	r.plugins[t] = p
}

// Get returns the SnipPlugin registered for snipType and true, or nil and
// false if no plugin has been registered for that type.
func (r *Registry) Get(snipType string) (SnipPlugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.plugins[snipType]
	return p, ok
}

// All returns every registered plugin in ascending order of their Type()
// string. The returned slice is a snapshot; later registrations are not
// reflected in it.
func (r *Registry) All() []SnipPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]SnipPlugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Type() < out[j].Type()
	})
	return out
}
