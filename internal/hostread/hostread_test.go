package hostread_test

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
)

// These are smoke tests against the real host running `go test` (Linux in CI).
// They assert shape and invariants, never fixed values; the arithmetic that
// encodes the specs is tested in the modules against faked readers.

// ADR-0005/0008: a reading starts nothing that outlives the call.
func TestReads_StartNoGoroutines(t *testing.T) {
	ctx := context.Background()
	before := runtime.NumGoroutine()
	for range 3 {
		_, _ = hostread.Memory{}.Read(ctx)
		_, _ = hostread.CPU{}.Times(ctx)
		_, _ = hostread.CPU{}.Counts(ctx)
		_, _ = hostread.CPU{}.Model(ctx)
		_, _ = hostread.Disk{}.Usage(ctx, rootPath())
		_, _ = hostread.Disk{}.Counters(ctx)
		if runtime.GOOS != "windows" { // spec 004 FR-018: never called there
			_, _ = hostread.CPU{}.LoadAvg(ctx)
		}
	}
	if after := runtime.NumGoroutine(); after != before {
		t.Fatalf("goroutines before=%d after=%d", before, after)
	}
}

// spec 005 NFR-006: the memory reading is plausible on this host.
func TestMemory_Read(t *testing.T) {
	r, err := hostread.Memory{}.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if r.Total == 0 {
		t.Fatal("total is zero")
	}
	if r.Available > r.Total {
		t.Fatalf("available %d exceeds total %d", r.Available, r.Total)
	}
}

// spec 004 NFR-006 regression: the CPU readings still work from here.
func TestCPU_Read(t *testing.T) {
	ctx := context.Background()
	ts, err := hostread.CPU{}.Times(ctx)
	if err != nil {
		t.Fatalf("times: %v", err)
	}
	if ts.Total() <= 0 {
		t.Fatalf("total CPU time %v", ts.Total())
	}
	if n, err := (hostread.CPU{}).Counts(ctx); err != nil || n < 1 {
		t.Fatalf("counts: %d %v", n, err)
	}
}

// rootPath is a path on the volume the OS runs from, on the platform running
// `go test`.
func rootPath() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("SystemDrive") + `\`
	}
	return "/"
}

// spec 008 NFR-006: the volume reading is plausible on this host. Available
// never exceeds the unreserved free space, so used + available ≤ total.
func TestDisk_Usage(t *testing.T) {
	u, err := hostread.Disk{}.Usage(context.Background(), rootPath())
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if u.Total == 0 {
		t.Fatal("total is zero")
	}
	if u.Used+u.Available > u.Total {
		t.Fatalf("used %d + available %d exceed total %d", u.Used, u.Available, u.Total)
	}
	if u.InodesFree > u.InodesTotal {
		t.Fatalf("free inodes %d exceed total %d", u.InodesFree, u.InodesTotal)
	}
	t.Logf("%s: total=%d used=%d available=%d inodes=%d/%d", u.Path, u.Total, u.Used, u.Available, u.InodesFree, u.InodesTotal)
}

// spec 008 FR-011, NFR-001: the counters are readable, classified and cheap.
// Some containers expose no block device at all, so an empty result skips.
func TestDisk_Counters(t *testing.T) {
	start := time.Now()
	cs, err := hostread.Disk{}.Counters(context.Background())
	took := time.Since(start)
	if err != nil {
		t.Fatalf("counters: %v", err)
	}
	if len(cs) == 0 {
		t.Skip("this host exposes no block device")
	}
	for i, c := range cs {
		if c.Kind == 0 {
			t.Errorf("%s: no kind", c.Name)
		}
		if i > 0 && cs[i-1].Name >= c.Name {
			t.Errorf("not sorted by name: %q before %q", cs[i-1].Name, c.Name)
		}
		t.Logf("%-12s kind=%v read=%d written=%d busy=%dms", c.Name, c.Kind, c.ReadBytes, c.WriteBytes, c.BusyMillis)
	}
	t.Logf("one reading took %v", took)
}
