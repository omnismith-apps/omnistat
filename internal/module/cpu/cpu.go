// Package cpu is omnistat's first metric provider (spec 004): aggregate CPU
// usage and the operating system's load averages as metrics, plus the CPU's
// model, logical core count and architecture as dimensions.
//
// It is also the first module whose value is a rate — a difference between two
// readings of a cumulative counter — so it keeps the previous reading between
// calls and primes itself on the first one (ADR-0006); and the first whose
// attributes are not all collectable everywhere, so its manifest declares
// per-attribute platform support (ADR-0007).
package cpu

import (
	"context"
	"log/slog"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// Name is the module name.
const Name = "cpu"

// Manifest keys (spec 004 FR-001). The operator overrides slugs, never keys.
const (
	KeyUsage  = "usage"
	KeyLoad1  = "load1"
	KeyLoad5  = "load5"
	KeyLoad15 = "load15"
	KeyModel  = "model"
	KeyCores  = "cores"
	KeyArch   = "arch"
)

// DefaultInterval is how often the module is collected unless overridden
// (FR-004): six observations per the default 60s publish.
const DefaultInterval = 10 * time.Second

// DefaultPrime is how long the first collection waits between its two readings
// so that a one-shot `omnistat run` publishes a real measurement (FR-012).
const DefaultPrime = 250 * time.Millisecond

// primeSlack is the headroom required on top of DefaultPrime before the module
// is willing to spend the deadline priming (FR-012, US-2/2).
const primeSlack = 50 * time.Millisecond

// loadPlatforms are the platforms whose kernel maintains load averages.
// Windows does not: what it offers is a sampled processor-queue-length average,
// a different quantity, and publishing it under these slugs would be a lie
// (FR-006, FR-018, ADR-0007).
var loadPlatforms = []string{"linux", "darwin"}

// Module is the cpu module. Reader, GOOS, Now and Sleep are injectable so that
// every requirement is testable without a real CPU, a real platform or a real
// pause (NFR-005).
type Module struct {
	Reader Reader
	GOOS   string
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
	// Prime is the first collection's sampling window; zero means DefaultPrime.
	Prime time.Duration
	// Log receives the one omission record per collection (FR-016); nil means
	// the default logger.
	Log *slog.Logger

	mu sync.Mutex
	// last is the previous reading, held for the lifetime of the process and
	// never persisted (FR-011).
	last *Times
}

// New returns the module bound to the real host.
func New() *Module {
	return &Module{
		Reader: hostread.CPU{},
		GOOS:   runtime.GOOS,
		Now:    time.Now,
		Sleep:  sleep,
		Prime:  DefaultPrime,
	}
}

// sleep waits for d or until ctx is done, whichever comes first.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Name implements module.Module.
func (m *Module) Name() string { return Name }

// DefaultInterval implements module.Provider (FR-004).
func (m *Module) DefaultInterval() time.Duration { return DefaultInterval }

// Manifest implements module.Module (FR-001).
func (m *Module) Manifest() manifest.Manifest {
	load := func(key, slug, name, window string) manifest.Attribute {
		return manifest.Attribute{
			Key: key, Slug: slug, Name: name, Kind: manifest.KindMetric,
			Description: "Run-queue load averaged over " + window + ", as the operating system reports it",
			Platforms:   loadPlatforms,
		}
	}
	return manifest.Manifest{
		Module: Name,
		Attributes: []manifest.Attribute{
			{
				Key: KeyUsage, Slug: "cpu_usage_pct", Name: "CPU usage", Kind: manifest.KindMetric,
				Description: "Percent of CPU time that was not idle, aggregated across all logical CPUs",
			},
			load(KeyLoad1, "load_avg_1", "Load average (1m)", "1 minute"),
			load(KeyLoad5, "load_avg_5", "Load average (5m)", "5 minutes"),
			load(KeyLoad15, "load_avg_15", "Load average (15m)", "15 minutes"),
			{
				Key: KeyModel, Slug: "cpu_model", Name: "CPU model", Kind: manifest.KindText,
				Description: "Model string reported by the operating system",
			},
			{
				Key: KeyCores, Slug: "cpu_cores", Name: "CPU cores", Kind: manifest.KindNumber,
				Description: "Number of logical CPUs usable by the operating system",
			},
			{
				Key: KeyArch, Slug: "cpu_arch", Name: "CPU architecture", Kind: manifest.KindList,
				Description: "Architecture this build targets",
				Options:     []string{"amd64", "arm64"},
			},
		},
	}
}

// Collect implements module.Provider (spec 004 FR-005…FR-016).
//
// Every value is gathered independently, so one unreadable source costs only
// the observations that depend on it (FR-015). The omissions are reported once,
// at the end, rather than one log line per value (FR-016). Only a collection
// that could read nothing at all is a provider failure.
func (m *Module) Collect(ctx context.Context) ([]module.Observation, error) {
	var obs []module.Observation
	var om module.Omissions

	// The architecture is a property of this build, not of the host, so it is
	// the one value no reading can take away. An option the manifest does not
	// declare is dropped by the core, where every other bad list value is
	// (FR-008, 003 FR-002).
	obs = append(obs, module.Observation{Key: KeyArch, Value: runtime.GOARCH})
	read := 0

	if model, err := m.Reader.Model(ctx); err != nil {
		om.Add(KeyModel, err)
	} else {
		read++
		if v := firstLine(model); v != "" {
			obs = append(obs, module.Observation{Key: KeyModel, Value: v})
		} else {
			om.Add(KeyModel, errNoModel)
		}
	}

	if n, err := m.Reader.Counts(ctx); err != nil {
		om.Add(KeyCores, err)
	} else {
		read++
		obs = append(obs, module.Observation{Key: KeyCores, Value: n})
	}

	// FR-006/FR-018: only where the operating system maintains load averages.
	// Elsewhere they are not collected and nothing is substituted for them.
	if manifest.Collectable(loadPlatforms, m.goos()) {
		if l, err := m.Reader.LoadAvg(ctx); err != nil {
			om.Add(KeyLoad1, err)
		} else {
			read++
			obs = append(obs,
				module.Observation{Key: KeyLoad1, Value: l.One},
				module.Observation{Key: KeyLoad5, Value: l.Five},
				module.Observation{Key: KeyLoad15, Value: l.Fifteen})
		}
	}

	usage, gotUsage, err := m.usage(ctx)
	switch {
	case err != nil:
		om.Add(KeyUsage, err)
	case gotUsage:
		read++
		obs = append(obs, module.Observation{Key: KeyUsage, Value: round2(usage)})
	default:
		// A reading happened, it just could not yield a percentage yet
		// (FR-012's skipped prime, FR-013's unusable pair). Not an omission
		// worth an operator's attention.
		read++
	}

	if read == 0 {
		// Nothing about this host could be read: an ordinary provider failure
		// (FR-015, 003 FR-010). `arch` alone does not make a collection.
		return nil, om.Err(Name)
	}
	om.Log(m.logger(), Name)
	return obs, nil
}

// usage returns the CPU usage percentage, whether there is one to report, and
// any error that prevented a reading (spec 004 FR-011…FR-014, ADR-0006).
//
// The retained reading is assigned exactly once on every path, so a cancelled
// or failed call leaves either the new reading or the old one as the baseline,
// never a mixture (FR-014).
func (m *Module) usage(ctx context.Context) (float64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, err := m.Reader.Times(ctx)
	if err != nil {
		return 0, false, err // baseline untouched
	}
	if m.last != nil {
		prev := *m.last
		m.last = &cur
		pct, ok := Percent(prev, cur)
		return pct, ok, nil
	}

	// First collection: prime by taking a second reading after a bounded
	// pause, so that a one-shot run publishes a real measurement (FR-012).
	if !m.canPrime(ctx) {
		m.last = &cur
		return 0, false, nil
	}
	if err := m.Sleep(ctx, m.prime()); err != nil {
		m.last = &cur // the pause was cut short; this reading is the baseline
		return 0, false, nil
	}
	cur2, err := m.Reader.Times(ctx)
	if err != nil {
		m.last = &cur
		return 0, false, nil
	}
	m.last = &cur2
	pct, ok := Percent(cur, cur2)
	return pct, ok, nil
}

// canPrime reports whether the call has time to spare for the prime pause.
// A deadline shorter than the pause means no usage this tick rather than a
// value measured over an unknown window (FR-012, US-2/2).
func (m *Module) canPrime(ctx context.Context) bool {
	dl, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return dl.Sub(m.now()) >= m.prime()+primeSlack
}

func (m *Module) prime() time.Duration {
	if m.Prime > 0 {
		return m.Prime
	}
	return DefaultPrime
}

func (m *Module) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
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

// round2 rounds a percentage to two decimal places, half away from zero: the
// precision the OS reports load averages with, and ample for a value that is
// charted and alerted on (FR-005). The full float64 of the division is noise.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// firstLine trims a reported string to its first non-empty line, so that a
// multi-line model string becomes one dimension value (FR-009).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
