package hostread_test

import (
	"context"
	"runtime"
	"testing"

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
