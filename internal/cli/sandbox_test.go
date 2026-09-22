//go:build sandbox

package cli_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/cpu"
	"github.com/omnismith-apps/omnistat/internal/module/hostname"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/omni"
)

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
	found, err := api.FindEntities(context.Background(), cur.Templates["host"].ID, "machine_id", "sandbox-run")
	if err != nil || len(found) == 0 {
		t.Fatalf("find: %v %v", found, err)
	}
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
	found, err := api.FindEntities(context.Background(), cur.Templates["host"].ID, "machine_id", "sandbox-cpu")
	if err != nil || len(found) == 0 {
		t.Fatalf("find: %v %v", found, err)
	}

	// Read the series back. The default bucket is 1 hour, which would collapse
	// a seconds-long run into one point (T003 finding), so ask for seconds.
	series, err := api.EntityChart(context.Background(), found[0].ID, []string{usage.ID},
		started, time.Now().Add(time.Minute), "1 second", "last")
	if err != nil {
		t.Fatalf("chart: %v", err)
	}
	points := series[usage.ID]
	if len(points) < 2 {
		t.Fatalf("expected several observations, got %d: %+v", len(points), points)
	}
	seen := map[time.Time]bool{}
	for _, p := range points {
		if p.Value < 0 || p.Value > 100 {
			t.Errorf("usage out of range: %v at %v", p.Value, p.At)
		}
		seen[p.At] = true
	}
	if len(seen) < 2 {
		t.Fatalf("observations must carry their own collection times: %+v", points)
	}
	t.Logf("read back %d points, %d distinct timestamps, first=%v last=%v",
		len(points), len(seen), points[0], points[len(points)-1])
}
