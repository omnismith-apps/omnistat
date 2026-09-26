package hostread_test

import (
	"context"
	"errors"
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
		_, _ = hostread.Net{}.Interfaces(ctx)
		_, _ = hostread.Net{}.Stack(ctx)
		_, _ = hostread.Net{}.ListenDrops(ctx)
		_, _ = hostread.Net{}.TimeWait(ctx)
		_, _ = hostread.Net{}.Conntrack(ctx)
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

// netSupported is where spec 010 reads the network at all (FR-018).
func netSupported() bool { return runtime.GOOS == "linux" || runtime.GOOS == "windows" }

// spec 010 FR-005, NFR-001: the interfaces are readable, classified and cheap.
func TestNet_Interfaces(t *testing.T) {
	start := time.Now()
	ifs, err := hostread.Net{}.Interfaces(context.Background())
	took := time.Since(start)
	if !netSupported() {
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("want ErrUnsupported, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("interfaces: %v", err)
	}
	for i, c := range ifs {
		if c.ID == "" || c.Name == "" {
			t.Errorf("interface %d has no id or name: %+v", i, c)
		}
		if i > 0 && ifs[i-1].ID >= c.ID {
			t.Errorf("not sorted by id: %q before %q", ifs[i-1].ID, c.ID)
		}
		t.Logf("%-16s id=%s physical=%v rx=%d tx=%d", c.Name, c.ID, c.Physical, c.RxBytes, c.TxBytes)
	}
	t.Logf("one reading took %v", took)
}

// spec 010 FR-010, FR-011, FR-013: the TCP/UDP counters are readable.
func TestNet_Stack(t *testing.T) {
	ctx := context.Background()
	s, err := hostread.Net{}.Stack(ctx)
	if !netSupported() {
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("want ErrUnsupported, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("stack: %v", err)
	}
	t.Logf("stack: %+v", s)
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := (hostread.Net{}).ListenDrops(ctx); err != nil {
		t.Errorf("listen drops: %v", err)
	}
	if _, err := (hostread.Net{}).TimeWait(ctx); err != nil {
		t.Errorf("time wait: %v", err)
	}
}

// spec 010 FR-014: connection tracking reads, or says it is not loaded; off
// Linux it is unsupported.
func TestNet_Conntrack(t *testing.T) {
	c, err := hostread.Net{}.Conntrack(context.Background())
	switch {
	case runtime.GOOS != "linux":
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("want ErrUnsupported, got %v", err)
		}
	case errors.Is(err, hostread.ErrNoConntrack):
		t.Log("connection tracking not loaded here")
	case err != nil:
		t.Fatalf("conntrack: %v", err)
	default:
		if c.Max == 0 {
			t.Fatalf("loaded but max is zero: %+v", c)
		}
		t.Logf("conntrack %d/%d", c.Count, c.Max)
	}
}
