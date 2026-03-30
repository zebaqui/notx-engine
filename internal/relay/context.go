package relay

import (
	"sync"
)

// ─────────────────────────────────────────────────────────────────────────────
// ExecutionContext — mutable state threaded through a flow execution
// ─────────────────────────────────────────────────────────────────────────────

// ExecutionContext holds the mutable state for a single relay execution or
// flow.  It is NOT safe for concurrent use from multiple goroutines; flows are
// executed sequentially and each RPC call gets its own context.
type ExecutionContext struct {
	mu        sync.Mutex
	variables map[string]string
	DeviceURN string
}

// NewExecutionContext creates an ExecutionContext seeded with the provided
// initial variable bindings and the device identity.
func NewExecutionContext(deviceURN string, initial map[string]string) *ExecutionContext {
	vars := make(map[string]string, len(initial))
	for k, v := range initial {
		vars[k] = v
	}
	return &ExecutionContext{
		DeviceURN: deviceURN,
		variables: vars,
	}
}

// Get returns the value of a variable by name, and whether it was found.
func (ec *ExecutionContext) Get(name string) (string, bool) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	v, ok := ec.variables[name]
	return v, ok
}

// Set stores or updates a variable value.
func (ec *ExecutionContext) Set(name, value string) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.variables[name] = value
}

// SetAll merges a map of key/value pairs into the variable store.
// Existing keys are overwritten.
func (ec *ExecutionContext) SetAll(vars map[string]string) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	for k, v := range vars {
		ec.variables[k] = v
	}
}

// Snapshot returns a copy of all current variable bindings.
// The returned map is safe to hand to callers without holding the lock.
func (ec *ExecutionContext) Snapshot() map[string]string {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	out := make(map[string]string, len(ec.variables))
	for k, v := range ec.variables {
		out[k] = v
	}
	return out
}
