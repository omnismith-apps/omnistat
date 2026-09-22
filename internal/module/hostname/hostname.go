// Package hostname is the reference value module (spec 003 FR-023…025): one
// text dimension carrying the operating system's hostname, which serves as
// the host entity's human-readable label (spec 002 FR-014).
package hostname

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// Name is the module name.
const Name = "hostname"

// AttributeKey is the manifest key of the hostname attribute.
const AttributeKey = "hostname"

// DefaultInterval is how often the hostname is collected unless overridden (FR-025).
const DefaultInterval = 5 * time.Minute

// Module is the hostname module. Hostname is injectable for tests.
type Module struct {
	Hostname func() (string, error)
}

// New returns the module bound to the real OS.
func New() *Module { return &Module{Hostname: os.Hostname} }

// Name implements module.Module.
func (m *Module) Name() string { return Name }

// Manifest implements module.Module (FR-023).
func (m *Module) Manifest() manifest.Manifest {
	return manifest.Manifest{
		Module: Name,
		Attributes: []manifest.Attribute{{
			Key:         AttributeKey,
			Name:        "Hostname",
			Slug:        "hostname",
			Kind:        manifest.KindText,
			Description: "Hostname reported by the operating system",
		}},
	}
}

// Collect implements module.Provider (FR-024): the OS hostname, trimmed; no
// DNS lookup. An empty value is a failure, never an observation.
func (m *Module) Collect(_ context.Context) ([]module.Observation, error) {
	h, err := m.Hostname()
	if err != nil {
		return nil, fmt.Errorf("hostname: %w", err)
	}
	h = strings.TrimSpace(h)
	if h == "" {
		return nil, errors.New("hostname: hostname is empty")
	}
	return []module.Observation{{Key: AttributeKey, Value: h}}, nil
}

// DefaultInterval implements module.Provider (FR-025).
func (m *Module) DefaultInterval() time.Duration { return DefaultInterval }
