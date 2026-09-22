// Package moduletest provides fixture modules for tests of the core.
package moduletest

import (
	"context"
	"time"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// Provided is a Static module with a provider whose behaviour tests script
// (spec 003). Fn and Interval may be left nil/zero.
type Provided struct {
	module.Static
	Interval time.Duration
	Fn       func(ctx context.Context) ([]module.Observation, error)
}

// Collect implements module.Provider via Fn; a nil Fn yields nothing.
func (p Provided) Collect(ctx context.Context) ([]module.Observation, error) {
	if p.Fn == nil {
		return nil, nil
	}
	return p.Fn(ctx)
}

// DefaultInterval implements module.Provider; zero means one minute.
func (p Provided) DefaultInterval() time.Duration {
	if p.Interval == 0 {
		return time.Minute
	}
	return p.Interval
}

// WithProvider wraps a Static module with a scripted provider.
func WithProvider(m module.Module, interval time.Duration, collect func(ctx context.Context) ([]module.Observation, error)) Provided {
	return Provided{Static: module.Static{M: m.Manifest()}, Interval: interval, Fn: collect}
}

// Probe is a fixture with a text, a metric and a list attribute on the host
// template. Its name and slugs are deliberately unlike any shipped module's,
// so that a test registry may hold it alongside the real ones.
func Probe() module.Module {
	return module.Static{M: manifest.Manifest{
		Module: "probe",
		Attributes: []manifest.Attribute{
			{Key: "model", Name: "Probe model", Slug: "probe_model", Kind: manifest.KindText, Description: "Model string as reported by the OS"},
			{Key: "usage", Name: "Probe usage", Slug: "probe_usage_pct", Kind: manifest.KindMetric, Description: "Percent busy"},
			{Key: "arch", Name: "Probe architecture", Slug: "probe_arch", Kind: manifest.KindList, Options: []string{"amd64", "arm64"}},
		},
	}}
}

// Disk is a fixture that introduces its own template plus one host attribute.
func Disk() module.Module {
	return module.Static{M: manifest.Manifest{
		Module:    "disk",
		Templates: []manifest.Template{{Slug: "disk", Name: "Disk", Description: "One per mounted filesystem"}},
		Attributes: []manifest.Attribute{
			{Key: "mount", Name: "Mount point", Slug: "disk_mount", Kind: manifest.KindText, Template: "disk"},
			{Key: "used", Name: "Used", Slug: "disk_used_pct", Kind: manifest.KindMetric, Template: "disk"},
			{Key: "count", Name: "Disk count", Slug: "disk_count", Kind: manifest.KindNumber},
		},
	}}
}

// Ident is a fixture standing in for the required identity module.
func Ident() module.Module {
	return module.Static{M: manifest.Manifest{
		Module:     "ident",
		Attributes: []manifest.Attribute{{Key: "id", Name: "Identity", Slug: "ident_id", Kind: manifest.KindText}},
	}}
}

// Registry returns a registry with Ident (required), Probe (on) and Disk (off by default).
func Registry() *module.Registry {
	r := module.NewRegistry()
	r.Register(Ident(), module.Required())
	r.Register(Probe())
	r.Register(Disk(), module.DisabledByDefault())
	return r
}
