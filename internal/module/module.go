// Package module defines what a module is to the core and keeps the registry
// of modules compiled into this build (spec 001 FR-006, US-4).
package module

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// Module is the unit of functionality: a manifest plus, in later features, a
// provider. Modules never talk to the Omnismith API.
type Module interface {
	// Name is the module's identifier, e.g. "cpu" or "machine-id".
	Name() string
	// Manifest declares the schema the module owns.
	Manifest() manifest.Manifest
}

// Option configures a registration.
type Option func(*entry)

// DisabledByDefault registers a module that runs only when enabled in config.
func DisabledByDefault() Option { return func(e *entry) { e.enabled = false } }

// Required registers a module that cannot be disabled (spec 002 FR-002).
func Required() Option { return func(e *entry) { e.required = true } }

type entry struct {
	mod      Module
	enabled  bool
	required bool
}

// Registry is the ordered set of modules available in this build.
type Registry struct {
	entries []entry
	index   map[string]int
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{index: map[string]int{}} }

// Register adds a module; enabled by default unless DisabledByDefault.
// Registering the same name twice panics — that is a programming error.
func (r *Registry) Register(m Module, opts ...Option) {
	name := m.Name()
	if _, dup := r.index[name]; dup {
		panic(fmt.Sprintf("module %q registered twice", name))
	}
	e := entry{mod: m, enabled: true}
	for _, o := range opts {
		o(&e)
	}
	r.index[name] = len(r.entries)
	r.entries = append(r.entries, e)
}

// Names lists all registered modules, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.entries))
	for _, e := range r.entries {
		names = append(names, e.mod.Name())
	}
	sort.Strings(names)
	return names
}

// Enabled applies the operator's on/off switches (name → enabled; absent
// names keep their default) and returns the enabled modules in registration
// order. An unknown name, or a required module switched off, is an error.
func (r *Registry) Enabled(switches map[string]bool) ([]Module, error) {
	var problems []string
	for name, on := range switches {
		i, ok := r.index[name]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("unknown module %q (known: %s)", name, strings.Join(r.Names(), ", ")))
		case !on && r.entries[i].required:
			problems = append(problems, fmt.Sprintf("module %q is required and cannot be disabled", name))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("modules: %s", strings.Join(problems, "; "))
	}
	var out []Module
	for _, e := range r.entries {
		on := e.enabled
		if v, set := switches[e.mod.Name()]; set {
			on = v
		}
		if on {
			out = append(out, e.mod)
		}
	}
	return out, nil
}

// Manifests collects the manifests of the given modules.
func Manifests(mods []Module) []manifest.Manifest {
	out := make([]manifest.Manifest, 0, len(mods))
	for _, m := range mods {
		out = append(out, m.Manifest())
	}
	return out
}

// Static is a Module backed by a fixed manifest; handy for fixtures and for
// modules whose provider lives elsewhere.
type Static struct{ M manifest.Manifest }

// Name implements Module.
func (s Static) Name() string { return s.M.Module }

// Manifest implements Module.
func (s Static) Manifest() manifest.Manifest { return s.M }
