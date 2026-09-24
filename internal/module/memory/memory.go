// Package memory reports how close a host is to swapping (spec 005): the
// share of physical memory in use and the memory still available without
// swapping as metrics, and the physical memory size as a dimension.
//
// Every value is a current level, not a rate, so unlike cpu the module keeps
// nothing between collections and never pauses (FR-010). "Available" is the
// operating system's own estimate; macOS maintains none, so there only the
// total is collected (FR-011, ADR-0007).
package memory

import (
	"context"
	"log/slog"
	"math"
	"runtime"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// Name is the module name.
const Name = "memory"

// Manifest keys (spec 005 FR-001). The operator overrides slugs, never keys.
const (
	KeyUsedPct   = "used_pct"
	KeyAvailable = "available"
	KeyTotal     = "total"
)

// DefaultInterval is how often the module is collected unless overridden
// (FR-004): memory is a level, and sustained pressure is what a "near swap"
// alert needs — two observations per default 60s publish.
const DefaultInterval = 30 * time.Second

// mibShift converts bytes to whole MiB, rounding down (FR-005, FR-006).
const mibShift = 20

// availablePlatforms are the platforms whose OS maintains its own estimate of
// memory available without swapping. macOS does not: any figure computed from
// its page counts leaves out compressed and purgeable memory, and publishing
// it under these slugs would be a different quantity (FR-011, ADR-0007).
var availablePlatforms = []string{"linux", "windows"}

// Module is the memory module. Reader and GOOS are injectable so that every
// requirement is testable without a real host or platform (NFR-004).
type Module struct {
	Reader Reader
	GOOS   string
	// Log receives the one omission record per collection (FR-013); nil means
	// the default logger.
	Log *slog.Logger
}

// New returns the module bound to the real host.
func New() *Module {
	return &Module{Reader: hostread.Memory{}, GOOS: runtime.GOOS}
}

// Name implements module.Module.
func (m *Module) Name() string { return Name }

// DefaultInterval implements module.Provider (FR-004).
func (m *Module) DefaultInterval() time.Duration { return DefaultInterval }

// Manifest implements module.Module (FR-001).
func (m *Module) Manifest() manifest.Manifest {
	return manifest.Manifest{
		Module: Name,
		Attributes: []manifest.Attribute{
			{
				Key: KeyUsedPct, Slug: "mem_used_pct", Name: "Memory used", Kind: manifest.KindMetric,
				Description: "Percent of physical memory not available without swapping",
				Platforms:   availablePlatforms,
			},
			{
				Key: KeyAvailable, Slug: "mem_available_mib", Name: "Memory available", Kind: manifest.KindMetric,
				Description: "Memory the operating system estimates can be given to programs without swapping, in MiB",
				Platforms:   availablePlatforms,
			},
			{
				Key: KeyTotal, Slug: "mem_total_mib", Name: "Memory total", Kind: manifest.KindNumber,
				Description: "Physical memory the operating system reports, in MiB",
			},
		},
	}
}

// Collect implements module.Provider (spec 005 FR-005…FR-013).
//
// One reading yields every value, so the percentage, the available amount and
// the total are always consistent with each other (FR-007). A reading that
// fails, or reports no memory at all, leaves nothing to publish and is an
// ordinary provider failure (FR-012). An inconsistent pair costs only the two
// values derived from it (FR-008), reported in one record (FR-013).
func (m *Module) Collect(ctx context.Context) ([]module.Observation, error) {
	var om module.Omissions

	r, err := m.Reader.Read(ctx)
	if err != nil {
		om.Add(KeyTotal, err)
		return nil, om.Err(Name)
	}
	if r.Total == 0 {
		om.Add(KeyTotal, errZeroTotal)
		return nil, om.Err(Name)
	}

	// FR-009: the total is observed on every collection.
	obs := []module.Observation{{Key: KeyTotal, Value: r.Total >> mibShift}}

	// FR-011: only where the OS maintains its own estimate. Elsewhere nothing
	// is collected and nothing is substituted; the core reports the skip once.
	if manifest.Collectable(availablePlatforms, m.goos()) {
		if r.Available > r.Total {
			om.Add(KeyUsedPct, errAvailableExceedsTotal)
			om.Add(KeyAvailable, errAvailableExceedsTotal)
		} else {
			obs = append(obs,
				module.Observation{Key: KeyUsedPct, Value: usedPct(r)},
				module.Observation{Key: KeyAvailable, Value: r.Available >> mibShift})
		}
	}

	om.Log(m.logger(), Name)
	return obs, nil
}

// usedPct is (total − available) ÷ total × 100 from the exact byte values,
// clamped to [0, 100] and rounded to two decimal places, half away from zero
// (FR-007). The caller guarantees 0 ≤ available ≤ total and total > 0; the
// clamp guards floating-point edges only.
func usedPct(r hostread.MemoryReading) float64 {
	pct := float64(r.Total-r.Available) / float64(r.Total) * 100
	return math.Round(min(max(pct, 0), 100)*100) / 100
}

func (m *Module) goos() string {
	if m.GOOS != "" {
		return m.GOOS
	}
	return runtime.GOOS
}

func (m *Module) logger() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}
