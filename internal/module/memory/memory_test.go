package memory_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/memory"
)

// FR-001: the manifest is the module's contract with the project schema, so it
// is pinned attribute by attribute. Changing a default slug is a breaking
// change that needs an ADR (001 FR-002).
func TestManifest_DefaultSlugs(t *testing.T) {
	m := memory.New().Manifest()
	if m.Module != "memory" {
		t.Fatalf("module name: %q", m.Module)
	}
	if len(m.Templates) != 0 {
		t.Fatalf("memory attaches to the host template, declaring none of its own: %+v", m.Templates)
	}
	want := []struct {
		key, slug, name string
		kind            manifest.Kind
		platforms       string
	}{
		{"used_pct", "mem_used_pct", "Memory used", manifest.KindMetric, "linux,windows"},
		{"available", "mem_available_mib", "Memory available", manifest.KindMetric, "linux,windows"},
		{"total", "mem_total_mib", "Memory total", manifest.KindNumber, ""},
	}
	if len(m.Attributes) != len(want) {
		t.Fatalf("got %d attributes, want %d: %+v", len(m.Attributes), len(want), m.Attributes)
	}
	for i, w := range want {
		a := m.Attributes[i]
		if a.Key != w.key || a.Slug != w.slug || a.Name != w.name || a.Kind != w.kind {
			t.Errorf("attribute %d: got %+v, want key=%s slug=%s name=%s kind=%s", i, a, w.key, w.slug, w.name, w.kind)
		}
		// FR-011: macOS maintains no available-memory estimate.
		if got := strings.Join(a.Platforms, ","); got != w.platforms {
			t.Errorf("%s platforms: got %q, want %q", w.key, got, w.platforms)
		}
		if len(a.Options) != 0 {
			t.Errorf("%s: no memory attribute is a list, got options %v", w.key, a.Options)
		}
		if strings.TrimSpace(a.Description) == "" {
			t.Errorf("%s: description is empty", w.key)
		}
		if a.Template != "" {
			t.Errorf("%s: must attach to the default host template, got %q", w.key, a.Template)
		}
	}
	// The manifest must satisfy the core's own rules (001 FR-004).
	if err := manifest.Validate([]manifest.Manifest{m}); err != nil {
		t.Fatalf("manifest must validate: %v", err)
	}
}

// FR-004: the default cadence is 30s — two observations per default publish.
func TestDefaultInterval(t *testing.T) {
	m := memory.New()
	if got := m.DefaultInterval(); got != 30*time.Second {
		t.Fatalf("default interval: %v", got)
	}
	if _, ok := module.ProviderOf(m); !ok {
		t.Fatal("memory must be a provider")
	}
	if got := m.Name(); got != memory.Name {
		t.Fatalf("Name() = %q, want %q", got, memory.Name)
	}
}

// FR-005, FR-006: amounts are whole MiB, rounded down — never up, so headroom
// is never overstated.
func TestCollect_FloorsToWholeMiB(t *testing.T) {
	m, _ := newTestModule(reading(16*1024*mib+mib-1, 3*mib+(mib-1)), "linux")
	got := collectOK(t, m)
	if got["total"] != uint64(16*1024) {
		t.Errorf("total = %v (%T), want 16384", got["total"], got["total"])
	}
	if got["available"] != uint64(3) {
		t.Errorf("available = %v (%T), want 3", got["available"], got["available"])
	}
}

// FR-007: the percentage comes from the exact byte values of the same
// reading, not from the floored MiB. With a total of 1.5 MiB and 1 MiB
// available, bytes give 33.33% used; floored MiB would give 1 − 1/1 = 0%.
func TestCollect_UsedPctFromBytesNotMiB(t *testing.T) {
	m, _ := newTestModule(reading(mib+mib/2, mib), "linux")
	got := collectOK(t, m)
	pct, ok := got["used_pct"].(float64)
	if !ok {
		t.Fatalf("used_pct = %v (%T), want a float64", got["used_pct"], got["used_pct"])
	}
	if pct != 33.33 {
		t.Fatalf("used_pct = %v, want 33.33", pct)
	}
}

// FR-007: the percentage is published rounded to two decimal places, not as
// the full float64 of the division (51.794210150282716 in the first sandbox
// run). Half rounds away from zero.
func TestCollect_UsedPctRoundedToTwoDecimals(t *testing.T) {
	cases := []struct {
		total, available uint64
		want             float64
	}{
		{3 * mib, mib, 66.67},             // 66.666…
		{8 * mib, 8*mib - mib/2000, 0.01}, // 0.00625 → 0.01
		{8 * mib, 8*mib - mib/4000, 0},    // 0.003125 → 0
		{7 * mib, 0, 100},
	}
	for _, c := range cases {
		m, _ := newTestModule(reading(c.total, c.available), "linux")
		if got := collectOK(t, m)["used_pct"]; got != c.want {
			t.Errorf("total=%d available=%d: used_pct = %v, want %v", c.total, c.available, got, c.want)
		}
	}
}

// FR-007: (total − available) ÷ total × 100 across the range, clamped.
func TestCollect_UsedPct(t *testing.T) {
	cases := []struct {
		name             string
		total, available uint64
		want             float64
	}{
		{"all available", 8 * 1024 * mib, 8 * 1024 * mib, 0},
		{"nothing available", 8 * 1024 * mib, 0, 100},
		{"a quarter used", 8 * 1024 * mib, 6 * 1024 * mib, 25},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _ := newTestModule(reading(c.total, c.available), "linux")
			got := collectOK(t, m)
			if pct := got["used_pct"].(float64); math.Abs(pct-c.want) > 1e-9 {
				t.Fatalf("used_pct = %v, want %v", pct, c.want)
			}
		})
	}
}

// FR-008, FR-012: a zero total is not a plausible reading; with nothing else
// to report, the collection is an ordinary provider failure.
func TestCollect_ZeroTotal_Fails(t *testing.T) {
	m, _ := newTestModule(reading(0, 0), "linux")
	obs, err := m.Collect(context.Background())
	if err == nil {
		t.Fatalf("want a failure, got %v", obs)
	}
	if !strings.Contains(err.Error(), "memory: nothing could be read") || !strings.Contains(err.Error(), "total") {
		t.Fatalf("error must name the module and the reason: %v", err)
	}
}

// FR-008, FR-013: available > total is inconsistent. It must not become 0%
// used; the pair is omitted, reported once, and total is still observed.
func TestCollect_AvailableExceedsTotal_OmitsPair(t *testing.T) {
	m, logs := newTestModule(reading(4*1024*mib, 5*1024*mib), "linux")
	got := collectOK(t, m)
	if _, ok := got["used_pct"]; ok {
		t.Errorf("used_pct must be omitted, got %v", got["used_pct"])
	}
	if _, ok := got["available"]; ok {
		t.Errorf("available must be omitted, got %v", got["available"])
	}
	if got["total"] != uint64(4*1024) {
		t.Errorf("total must still be observed, got %v", got["total"])
	}
	out := logs.String()
	if n := strings.Count(out, "observations omitted"); n != 1 {
		t.Fatalf("want one omission record, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "keys=used_pct,available") || !strings.Contains(out, "module=memory") {
		t.Errorf("record must name both keys and the module:\n%s", out)
	}
}

// FR-012: a reading that fails costs everything that depends on it — here,
// everything — and so fails the collection.
func TestCollect_ReadError_Fails(t *testing.T) {
	m, logs := newTestModule(&fakeReader{err: errRead}, "linux")
	obs, err := m.Collect(context.Background())
	if err == nil {
		t.Fatalf("want a failure, got %v", obs)
	}
	if !strings.Contains(err.Error(), errRead.Error()) {
		t.Fatalf("failure must carry the reason: %v", err)
	}
	// The failure is reported by the core (003 FR-010), not also as omissions.
	if strings.Contains(logs.String(), "observations omitted") {
		t.Errorf("a failed collection must not also log omissions:\n%s", logs.String())
	}
}

// FR-011, US-4/1: on macOS only the total is collected, and the attributes it
// cannot collect are not omissions — the core reports them once at startup.
func TestCollect_Darwin_TotalOnly(t *testing.T) {
	m, logs := newTestModule(reading(16*1024*mib, 8*1024*mib), "darwin")
	got := collectOK(t, m)
	if len(got) != 1 || got["total"] != uint64(16*1024) {
		t.Fatalf("darwin must observe total only, got %v", got)
	}
	if logs.Len() != 0 {
		t.Errorf("unsupported attributes must not be logged per collection:\n%s", logs.String())
	}
}

// FR-011: Windows maintains an available-memory estimate, so all three are
// collected there.
func TestCollect_Windows_AllThree(t *testing.T) {
	m, _ := newTestModule(reading(8*1024*mib, 2*1024*mib), "windows")
	got := collectOK(t, m)
	if got["total"] != uint64(8*1024) || got["available"] != uint64(2*1024) || got["used_pct"] != 75.0 {
		t.Fatalf("windows: got %v", got)
	}
}

// FR-009, FR-010: every collection takes exactly one reading and observes the
// total again; nothing is carried between collections.
func TestCollect_OneReadingPerCollection_TotalEveryTime(t *testing.T) {
	f := &fakeReader{readings: []hostread.MemoryReading{
		{Total: 8 * 1024 * mib, Available: 4 * 1024 * mib},
		{Total: 16 * 1024 * mib, Available: 4 * 1024 * mib}, // VM resized (US-3/2)
	}}
	m, _ := newTestModule(f, "linux")
	first := collectOK(t, m)
	if f.calls != 1 {
		t.Fatalf("first collection took %d readings, want 1", f.calls)
	}
	second := collectOK(t, m)
	if f.calls != 2 {
		t.Fatalf("second collection took %d readings in total, want 2", f.calls)
	}
	if first["total"] != uint64(8*1024) || second["total"] != uint64(16*1024) {
		t.Fatalf("total must be observed on every collection: %v then %v", first["total"], second["total"])
	}
	if first["used_pct"] != 50.0 || second["used_pct"] != 75.0 {
		t.Fatalf("used_pct must come from each collection's own reading: %v then %v", first["used_pct"], second["used_pct"])
	}
}

// A healthy collection logs nothing (FR-013) and returns exactly the three
// declared keys.
func TestCollect_Healthy(t *testing.T) {
	m, logs := newTestModule(reading(32*1024*mib, 20*1024*mib), "linux")
	got := collectOK(t, m)
	if len(got) != 3 {
		t.Fatalf("want 3 observations, got %v", got)
	}
	if logs.Len() != 0 {
		t.Errorf("healthy collection logged:\n%s", logs.String())
	}
}

// FR-012: a cancelled context reaches the reader and fails the collection;
// the module adds no pause of its own (FR-010).
func TestCollect_HonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m, _ := newTestModule(ctxReader{}, "linux")
	if _, err := m.Collect(ctx); err == nil {
		t.Fatal("a cancelled collection whose reading failed must fail")
	}
}

type ctxReader struct{}

func (ctxReader) Read(ctx context.Context) (hostread.MemoryReading, error) {
	if err := ctx.Err(); err != nil {
		return hostread.MemoryReading{}, err
	}
	return hostread.MemoryReading{Total: mib, Available: mib}, nil
}

// The real module is wired to the real host and platform.
func TestNew_UsesHost(t *testing.T) {
	m := memory.New()
	if _, ok := m.Reader.(hostread.Memory); !ok {
		t.Fatalf("Reader = %T, want hostread.Memory", m.Reader)
	}
	if m.GOOS == "" {
		t.Fatal("GOOS must default to the running platform")
	}
}
