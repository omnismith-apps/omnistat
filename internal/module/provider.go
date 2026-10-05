package module

import (
	"context"
	"time"
)

// Observation is one value a provider reports for one of its manifest's
// attributes, identified by the attribute's Key (spec 003 FR-001). The
// core types, stamps and buffers it; the provider never sees a slug, a
// timestamp or the API (ADR-0005).
type Observation struct {
	Key   string
	Value any
	// Entity is the module-owned entity the value belongs to; the zero value
	// is the host entity (spec 011 FR-006).
	Entity Entity
}

// Entity names a module-owned entity (spec 011 FR-006): one of the module's
// entity templates, by its manifest slug, and the key that identifies the
// record within it. The provider never sees an entity id.
type Entity struct {
	Template string
	Key      string
}

// IsHost reports whether e is the zero value, the host entity.
func (e Entity) IsHost() bool { return e == Entity{} }

// Provider is the optional value side of a module (spec 003 FR-001…004).
// Collect is called by the core on demand, under a context whose deadline is
// the module's collection interval; it must not start timers or goroutines
// that outlive the call. DefaultInterval is the cadence the core uses unless
// the operator overrides it.
type Provider interface {
	Collect(ctx context.Context) ([]Observation, error)
	DefaultInterval() time.Duration
}

// ProviderOf returns the module's provider, if it has one.
func ProviderOf(m Module) (Provider, bool) {
	p, ok := m.(Provider)
	return p, ok
}
