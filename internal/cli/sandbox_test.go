//go:build sandbox

package cli_test

import (
	"bytes"
	"context"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/cpu"
	"github.com/omnismith-apps/omnistat/internal/module/hostname"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/module/memory"
	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/settle"
)

// eventually reads until ok accepts the result, for up to ~10s. Omnismith
// processes writes asynchronously — search, entity reads and metric series
// all lag behind the write that the API already acknowledged (see
// docs/reference/omnismith-api-notes.md) — so no read-back in these tests may
// assume its write is visible on the first try.
func eventually[T any](t *testing.T, what string, read func(context.Context) (T, error), ok func(T) bool) T {
	t.Helper()
	delays := make([]time.Duration, 40)
	for i := range delays {
		delays[i] = 250 * time.Millisecond
	}
	got, settled, err := settle.Until(context.Background(), settle.Policy{Delays: delays}, read, ok)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if !settled {
		t.Fatalf("%s: not visible after waiting; last read: %+v", what, got)
	}
	return got
}

// TestSandbox_Run runs the real one-shot `run` against a real project with a
// throwaway static identity: it creates (or reuses) one host entity and
// publishes the hostname, then checks the value through the API. Nothing is
// deleted (constitution IV). Needs OMNISMITH_ACCESS_TOKEN / _PROJECT_ID /
// _BASE_URL and a reconciled schema.
//
//	make sandbox
func TestSandbox_Run(t *testing.T) {
	s, err := config.Load("", os.Getenv)
	if err != nil || s.RequireAPI() != nil {
		t.Skip("sandbox credentials not set")
	}
	reg := module.NewRegistry()
	reg.Register(machineid.New(), module.Required())
	hn := hostname.New()
	hn.Hostname = func() (string, error) { return "sandbox-host", nil }
	reg.Register(hn)
	app := &cli.App{Registry: reg, Version: "sandbox"}
	env := func(k string) string {
		if k == config.EnvIdentity {
			return "sandbox-run"
		}
		return os.Getenv(k)
	}

	var out, errb bytes.Buffer
	if code := app.Run(context.Background(), []string{"run", "--dry-run"}, &out, &errb, env); code != 0 || !strings.Contains(out.String(), `hostname = "sandbox-host"`) {
		t.Fatalf("dry-run: code=%d\n%s%s", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := app.Run(context.Background(), []string{"run"}, &out, &errb, env); code != 0 || !strings.Contains(out.String(), "published 1 dimensions, 0 observations") {
		t.Fatalf("run: code=%d\n%s%s", code, out.String(), errb.String())
	}
	t.Log(strings.TrimSpace(out.String()))

	// Read back: the entity with identity "sandbox-run" carries the hostname.
	api, err := omni.New(omni.Settings{BaseURL: s.BaseURL, Token: s.Token, ProjectID: s.ProjectID, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := api.ReadSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := eventually(t, "find sandbox-run", func(ctx context.Context) ([]identity.EntitySummary, error) {
		return api.FindEntities(ctx, cur.Templates["host"].ID, "machine_id", "sandbox-run")
	}, func(es []identity.EntitySummary) bool { return len(es) > 0 })
	eventually(t, "hostname read back", func(ctx context.Context) (map[string]string, error) {
		return api.EntityValues(ctx, found[0].ID, "hostname")
	}, func(v map[string]string) bool { return v["hostname"] == "sandbox-host" })
}

// TestSandbox_CPU proves the metric path end to end against a real project
// (spec 004 NFR-006), which feature 003 could only exercise against the fake
// because no module produced a metric yet: schema apply creates a metric
// attribute, a short daemon run ingests several observations on the module's
// own cadence, and the series is read back with their timestamps.
//
// It uses its own identity so it never touches another test's entity, and it
// deletes nothing (constitution IV).
//
//	make sandbox
func TestSandbox_CPU(t *testing.T) {
	s, err := config.Load("", os.Getenv)
	if err != nil || s.RequireAPI() != nil {
		t.Skip("sandbox credentials not set")
	}
	reg := module.NewRegistry()
	reg.Register(machineid.New(), module.Required())
	reg.Register(cpu.New())
	app := &cli.App{Registry: reg, Version: "sandbox"}
	env := func(k string) string {
		if k == config.EnvIdentity {
			return "sandbox-cpu"
		}
		return os.Getenv(k)
	}

	cfg := t.TempDir() + "/omnistat.yaml"
	// Fast grids so the run is short; the daemon publishes after the first
	// collection and then every 2s (003 FR-014).
	if err := os.WriteFile(cfg, []byte("modules:\n  cpu:\n    interval: 1s\npublish:\n  interval: 2s\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := app.Run(context.Background(), []string{"-config", cfg, "schema", "apply"}, &out, &errb, env); code != 0 {
		t.Fatalf("schema apply: code=%d\n%s%s", code, out.String(), errb.String())
	}
	t.Log(strings.TrimSpace(out.String()))

	started := time.Now().Add(-time.Minute)
	out.Reset()
	errb.Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	// The daemon stops on ctx (003 FR-020) and flushes what it buffered.
	if code := app.Run(ctx, []string{"-config", cfg, "run", "--daemon"}, &out, &errb, env); code != 0 {
		t.Fatalf("run --daemon: code=%d\n%s%s", code, out.String(), errb.String())
	}
	t.Log(strings.TrimSpace(out.String()))

	api, err := omni.New(omni.Settings{BaseURL: s.BaseURL, Token: s.Token, ProjectID: s.ProjectID, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := api.ReadSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	usage, ok := cur.Attributes["cpu_usage_pct"]
	if !ok {
		t.Fatal("cpu_usage_pct was not created")
	}
	if usage.Type != "metric" {
		t.Fatalf("cpu_usage_pct must be a metric, got %q", usage.Type)
	}
	found := eventually(t, "find sandbox-cpu", func(ctx context.Context) ([]identity.EntitySummary, error) {
		return api.FindEntities(ctx, cur.Templates["host"].ID, "machine_id", "sandbox-cpu")
	}, func(es []identity.EntitySummary) bool { return len(es) > 0 })

	// Read the series back. The default bucket is 1 hour, which would collapse
	// a seconds-long run into one point (T003 finding), so ask for seconds.
	// Ingestion is asynchronous (202), so wait until several points are in.
	series := eventually(t, "cpu_usage_pct series", func(ctx context.Context) (map[string][]omni.ChartPoint, error) {
		return api.EntityChart(ctx, found[0].ID, []string{usage.ID}, started, time.Now().Add(time.Minute), "1 second", "last")
	}, func(m map[string][]omni.ChartPoint) bool { return distinct(m[usage.ID]) >= 2 })
	points := series[usage.ID]
	if len(points) < 2 {
		t.Fatalf("expected several observations, got %d: %+v", len(points), points)
	}
	seen := map[time.Time]bool{}
	for _, p := range points {
		if p.Value < 0 || p.Value > 100 || !twoDecimals(p.Value) {
			t.Errorf("usage out of range or not rounded to two decimals (004 FR-005): %v at %v", p.Value, p.At)
		}
		seen[p.At] = true
	}
	if len(seen) < 2 {
		t.Fatalf("observations must carry their own collection times: %+v", points)
	}
	t.Logf("read back %d points, %d distinct timestamps, first=%v last=%v",
		len(points), len(seen), points[0], points[len(points)-1])
}

// TestSandbox_Memory proves the memory module end to end (spec 005 NFR-005):
// the schema is applied, the daemon collects every second and publishes every
// two, and both metrics are read back as series and the total as a dimension.
// Linux only: the expected total is read from /proc/meminfo by the test.
func TestSandbox_Memory(t *testing.T) {
	s, err := config.Load("", os.Getenv)
	if err != nil || s.RequireAPI() != nil {
		t.Skip("sandbox credentials not set")
	}
	wantTotal, err := memTotalMiB()
	if err != nil {
		t.Skipf("needs /proc/meminfo: %v", err)
	}
	reg := module.NewRegistry()
	reg.Register(machineid.New(), module.Required())
	reg.Register(memory.New())
	app := &cli.App{Registry: reg, Version: "sandbox"}
	env := func(k string) string {
		if k == config.EnvIdentity {
			return "sandbox-memory"
		}
		return os.Getenv(k)
	}

	cfg := t.TempDir() + "/omnistat.yaml"
	if err := os.WriteFile(cfg, []byte("modules:\n  memory:\n    interval: 1s\npublish:\n  interval: 2s\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := app.Run(context.Background(), []string{"-config", cfg, "schema", "apply"}, &out, &errb, env); code != 0 {
		t.Fatalf("schema apply: code=%d\n%s%s", code, out.String(), errb.String())
	}
	t.Log(strings.TrimSpace(out.String()))

	started := time.Now().Add(-time.Minute)
	out.Reset()
	errb.Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	if code := app.Run(ctx, []string{"-config", cfg, "run", "--daemon"}, &out, &errb, env); code != 0 {
		t.Fatalf("run --daemon: code=%d\n%s%s", code, out.String(), errb.String())
	}

	api, err := omni.New(omni.Settings{BaseURL: s.BaseURL, Token: s.Token, ProjectID: s.ProjectID, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := api.ReadSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for slug, typ := range map[string]string{"mem_used_pct": "metric", "mem_available_mib": "metric", "mem_total_mib": "number"} {
		a, ok := cur.Attributes[slug]
		if !ok {
			t.Fatalf("%s was not created", slug)
		}
		if a.Type != typ {
			t.Fatalf("%s must be %s, got %q", slug, typ, a.Type)
		}
	}
	found := eventually(t, "find sandbox-memory", func(ctx context.Context) ([]identity.EntitySummary, error) {
		return api.FindEntities(ctx, cur.Templates["host"].ID, "machine_id", "sandbox-memory")
	}, func(es []identity.EntitySummary) bool { return len(es) > 0 })
	host := found[0].ID

	// US-3/1: the dimension, read back from the entity once the write is
	// visible (writes are processed asynchronously).
	vals := eventually(t, "mem_total_mib read back", func(ctx context.Context) (map[string]string, error) {
		return api.EntityValues(ctx, host, "mem_total_mib")
	}, func(v map[string]string) bool { return v["mem_total_mib"] != "" })
	gotTotal, err := strconv.ParseFloat(vals["mem_total_mib"], 64)
	if err != nil || uint64(gotTotal) != wantTotal {
		t.Fatalf("mem_total_mib = %q, want %d (from /proc/meminfo)", vals["mem_total_mib"], wantTotal)
	}

	// US-1/1, US-2/1: both metrics, read back as series with their own times,
	// once asynchronous ingestion has caught up.
	used, avail := cur.Attributes["mem_used_pct"].ID, cur.Attributes["mem_available_mib"].ID
	series := eventually(t, "memory series", func(ctx context.Context) (map[string][]omni.ChartPoint, error) {
		return api.EntityChart(ctx, host, []string{used, avail}, started, time.Now().Add(time.Minute), "1 second", "last")
	}, func(m map[string][]omni.ChartPoint) bool { return distinct(m[used]) >= 2 && distinct(m[avail]) >= 2 })
	for id, check := range map[string]func(float64) bool{
		used:  func(v float64) bool { return v >= 0 && v <= 100 && twoDecimals(v) },
		avail: func(v float64) bool { return v > 0 && v <= float64(wantTotal) && v == float64(uint64(v)) },
	} {
		points := series[id]
		seen := map[time.Time]bool{}
		for _, p := range points {
			if !check(p.Value) {
				t.Errorf("implausible value %v at %v for %s", p.Value, p.At, id)
			}
			seen[p.At] = true
		}
		if len(seen) < 2 {
			t.Fatalf("expected several observations with distinct times for %s, got %+v", id, points)
		}
		t.Logf("%s: %d points, %d distinct timestamps, first=%v last=%v", id, len(points), len(seen), points[0], points[len(points)-1])
	}
	t.Logf("mem_total_mib read back as %q", vals["mem_total_mib"])
}

// twoDecimals reports whether v was published with at most two decimal
// places. The chart returns values through float32, so allow its error.
func twoDecimals(v float64) bool {
	return math.Abs(v*100-math.Round(v*100)) < 1e-3
}

// distinct counts the distinct timestamps in a series.
func distinct(points []omni.ChartPoint) int {
	seen := map[time.Time]bool{}
	for _, p := range points {
		seen[p.At] = true
	}
	return len(seen)
}

// memTotalMiB is MemTotal from /proc/meminfo in whole MiB — what spec 005
// FR-006 says the module must publish on Linux.
func memTotalMiB() (uint64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				return 0, err
			}
			return kb * 1024 >> 20, nil
		}
	}
	return 0, os.ErrNotExist
}
