package disk_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/disk"
)

const (
	mib = uint64(1) << 20
	gib = uint64(1) << 30
)

// fakeReader scripts the host. Counters readings are consumed in order and the
// last one repeats, so a test that knows the first collection primes (two
// readings) indexes accordingly. It is safe for concurrent use so that -race
// reports contention inside the module, not inside the fake.
type fakeReader struct {
	mu sync.Mutex

	usage      hostread.VolumeUsage
	usageErr   error
	usagePaths []string

	counters    [][]hostread.DiskCounters
	countersErr error
	counterN    int

	windowsDir    string
	windowsDirErr error
	dirs          map[string]bool
}

func (f *fakeReader) Usage(_ context.Context, path string) (hostread.VolumeUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usagePaths = append(f.usagePaths, path)
	if f.usageErr != nil {
		return hostread.VolumeUsage{}, f.usageErr
	}
	u := f.usage
	u.Path = path
	return u, nil
}

func (f *fakeReader) Counters(context.Context) ([]hostread.DiskCounters, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.countersErr != nil {
		return nil, f.countersErr
	}
	if len(f.counters) == 0 {
		return nil, nil
	}
	c := f.counters[min(f.counterN, len(f.counters)-1)]
	f.counterN++
	return c, nil
}

func (f *fakeReader) WindowsDir(context.Context) (string, error) {
	if f.windowsDirErr != nil {
		return "", f.windowsDirErr
	}
	return f.windowsDir, nil
}

func (f *fakeReader) DirExists(path string) bool { return f.dirs[path] }

func (f *fakeReader) counterCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counterN
}

var errRead = errors.New("reading unavailable")

// vol is a system volume: sizes in bytes, and inode counts.
func vol(total, used, available, inodesTotal, inodesFree uint64) hostread.VolumeUsage {
	return hostread.VolumeUsage{Total: total, Used: used, Available: available, InodesTotal: inodesTotal, InodesFree: inodesFree}
}

// healthyVolume is a 100 GiB ext4-like volume, 40% used, with inodes.
var healthyVolume = vol(100*gib, 40*gib, 60*gib, 1000, 400)

// dev is one device's cumulative counters.
func dev(name string, kind hostread.DeviceKind, readB, writeB, readOps, writeOps, busyMs uint64) hostread.DiskCounters {
	return hostread.DiskCounters{Name: name, Kind: kind, ReadBytes: readB, WriteBytes: writeB, ReadOps: readOps, WriteOps: writeOps, BusyMillis: busyMs}
}

func devs(d ...hostread.DiskCounters) []hostread.DiskCounters { return d }

// fakeClock is the module's injected time: Sleep advances it instantly and
// records the pause, so no test ever really waits (NFR-004).
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
	// sleepErr, when set, makes the pause fail as a cancelled one would.
	sleepErr error
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	if c.sleepErr != nil {
		return c.sleepErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var t0 = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// newTestModule builds the module on a fake host, platform and clock, logging
// every level into the returned buffer.
func newTestModule(r *fakeReader, goos string) (*disk.Module, *fakeClock, *bytes.Buffer) {
	var buf bytes.Buffer
	clk := &fakeClock{now: t0}
	return &disk.Module{
		Reader: r,
		GOOS:   goos,
		Now:    clk.Now,
		Sleep:  clk.Sleep,
		Prime:  disk.DefaultPrime,
		Log:    slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, clk, &buf
}

// collectOK collects once and returns the observations keyed by manifest key.
func collectOK(t *testing.T, m *disk.Module) map[string]any {
	t.Helper()
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return byKey(t, obs)
}

func byKey(t *testing.T, obs []module.Observation) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, o := range obs {
		if _, dup := out[o.Key]; dup {
			t.Fatalf("key %q observed twice in one collection", o.Key)
		}
		out[o.Key] = o.Value
	}
	return out
}

// value returns a float observation or fails the test.
func value(t *testing.T, got map[string]any, key string) float64 {
	t.Helper()
	v, ok := got[key]
	if !ok {
		t.Fatalf("%s not observed; got %v", key, got)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("%s: want float64, got %T", key, v)
	}
	return f
}

// absent fails the test if any of keys was observed.
func absent(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := got[k]; ok {
			t.Errorf("%s must not be observed, got %v", k, v)
		}
	}
}

// omissionRecords counts the one-per-collection omission records (FR-020).
func omissionRecords(buf *bytes.Buffer) int {
	return strings.Count(buf.String(), "observations omitted")
}

var ioKeys = []string{disk.KeyReadMiBps, disk.KeyWriteMiBps, disk.KeyReadIOPS, disk.KeyWriteIOPS, disk.KeyBusyPct}
