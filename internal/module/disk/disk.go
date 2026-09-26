// Package disk reports whether the system volume is filling up and how hard
// the physical disks are working (spec 008): the system volume's used share,
// available space and inode usage as metrics and its size as a dimension, and
// the read/write throughput, operation rates and busiest-disk utilisation of
// the host's physical disks as metrics.
//
// The I/O values are rates over cumulative counters, so, like cpu, the module
// keeps its previous reading and primes itself on the first collection
// (ADR-0006). Busy time and inodes are only maintained honestly on Linux, so
// the manifest gates them there (ADR-0007).
package disk

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
const Name = "disk"

// Manifest keys (spec 008 FR-001). The operator overrides slugs, never keys.
const (
	KeyRootUsedPct       = "root_used_pct"
	KeyRootAvailable     = "root_available"
	KeyRootTotal         = "root_total"
	KeyRootInodesUsedPct = "root_inodes_used_pct"
	KeyReadMiBps         = "read_mibps"
	KeyWriteMiBps        = "write_mibps"
	KeyReadIOPS          = "read_iops"
	KeyWriteIOPS         = "write_iops"
	KeyBusyPct           = "busy_pct"
)

// DefaultInterval is how often the module is collected unless overridden
// (FR-004): the rates become 30-second averages, two points per default publish.
const DefaultInterval = 30 * time.Second

// DefaultPrime is how long the first collection waits between its two counter
// readings so that a one-shot `omnistat run` publishes real rates (FR-014).
const DefaultPrime = 250 * time.Millisecond

// primeSlack is the headroom required on top of the prime before the module
// is willing to spend the deadline priming (FR-014).
const primeSlack = 50 * time.Millisecond

// linuxOnly gates the values only Linux maintains (FR-016). Windows keeps its
// idle time per volume and it is not among the readings taken; macOS keeps no
// busy time at all. NTFS has no inode limit, and APFS allocates inodes
// dynamically, so its figure is always near 0% and is not a limit.
var linuxOnly = []string{"linux"}

// Module is the disk module. Reader, GOOS, Now and Sleep are injectable so
// that every requirement is testable without a real disk, platform or pause
// (NFR-004).
type Module struct {
	Reader Reader
	GOOS   string
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
	// Prime is the first collection's sampling window; zero means DefaultPrime.
	Prime time.Duration
	// Log receives the omission record, the inode notice and the debug
	// record; nil means the default logger.
	Log *slog.Logger

	mu sync.Mutex
	// last is the previous counter reading of the counted devices, held for
	// the lifetime of the process and never persisted (FR-014).
	last *ioReading
	// inodeNotice makes the "no inode limit" record once per process (FR-009).
	inodeNotice sync.Once
}

// New returns the module bound to the real host.
func New() *Module {
	return &Module{
		Reader: hostread.Disk{},
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

// Manifest implements module.Module (FR-001, FR-016).
func (m *Module) Manifest() manifest.Manifest {
	metric := func(key, slug, name, desc string, platforms []string) manifest.Attribute {
		return manifest.Attribute{Key: key, Slug: slug, Name: name, Kind: manifest.KindMetric, Description: desc, Platforms: platforms}
	}
	return manifest.Manifest{
		Module: Name,
		Attributes: []manifest.Attribute{
			metric(KeyRootUsedPct, "disk_root_used_pct", "System volume used",
				"Percent of the system volume in use, as df computes it", nil),
			metric(KeyRootAvailable, "disk_root_available_gib", "System volume available",
				"Space on the system volume an unprivileged program can still write, in GiB", nil),
			{
				Key: KeyRootTotal, Slug: "disk_root_total_gib", Name: "System volume size", Kind: manifest.KindNumber,
				Description: "Size of the system volume, in GiB",
			},
			metric(KeyRootInodesUsedPct, "disk_root_inodes_used_pct", "System volume inodes used",
				"Percent of the system volume's inodes in use", linuxOnly),
			metric(KeyReadMiBps, "disk_read_mibps", "Disk read throughput",
				"Bytes read from the physical disks per second, in MiB/s", nil),
			metric(KeyWriteMiBps, "disk_write_mibps", "Disk write throughput",
				"Bytes written to the physical disks per second, in MiB/s", nil),
			metric(KeyReadIOPS, "disk_read_iops", "Disk read operations",
				"Read operations completed by the physical disks per second", nil),
			metric(KeyWriteIOPS, "disk_write_iops", "Disk write operations",
				"Write operations completed by the physical disks per second", nil),
			metric(KeyBusyPct, "disk_busy_pct", "Busiest disk utilisation",
				"Percent of wall time the busiest physical disk had I/O in progress", linuxOnly),
		},
	}
}

// Collect implements module.Provider (spec 008 FR-005…FR-022).
//
// The system volume and the I/O counters are read independently, so either
// can fail and cost only what depends on it (FR-017). Omissions are reported
// in one record at the end (FR-020); only a collection that produced nothing
// at all is a provider failure (FR-018).
func (m *Module) Collect(ctx context.Context) ([]module.Observation, error) {
	var om module.Omissions
	goos := m.goos()
	withInodes := manifest.Collectable(linuxOnly, goos)
	withBusy := manifest.Collectable(linuxOnly, goos)

	path, err := m.systemVolume(ctx, goos)
	var u hostread.VolumeUsage
	if err == nil {
		u, err = m.Reader.Usage(ctx, path)
	}
	var obs []module.Observation
	if err != nil {
		addAll(&om, err, spaceKeys(withInodes)...)
	} else {
		obs = append(obs, m.space(u, withInodes, &om)...)
	}

	rates, devices, err := m.io(ctx, withBusy)
	if err != nil {
		addAll(&om, err, ioKeys(withBusy)...)
	} else {
		obs = append(obs, rates...)
	}

	// FR-022: what was measured, for an operator checking the numbers. The
	// values themselves are logged by the core at debug (003 FR-026).
	m.logger().Debug("disk read", "module", Name, "path", path, "devices", strings.Join(devices, ","))

	if len(obs) == 0 {
		return nil, om.Err(Name)
	}
	om.Log(m.logger(), Name)
	return obs, nil
}

func spaceKeys(withInodes bool) []string {
	keys := []string{KeyRootUsedPct, KeyRootAvailable, KeyRootTotal}
	if withInodes {
		keys = append(keys, KeyRootInodesUsedPct)
	}
	return keys
}

func ioKeys(withBusy bool) []string {
	keys := []string{KeyReadMiBps, KeyWriteMiBps, KeyReadIOPS, KeyWriteIOPS}
	if withBusy {
		keys = append(keys, KeyBusyPct)
	}
	return keys
}

func addAll(om *module.Omissions, err error, keys ...string) {
	for _, k := range keys {
		om.Add(k, err)
	}
}

// round2 rounds to two decimal places, half away from zero (FR-007, FR-012).
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// pct is part ÷ whole × 100, clamped to [0, 100] and rounded to two decimal
// places (FR-007, FR-009, FR-013). The caller guarantees whole > 0.
func pct(part, whole float64) float64 {
	return round2(min(max(part/whole*100, 0), 100))
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
