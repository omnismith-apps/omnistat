//go:build sandbox && linux

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

	"golang.org/x/sys/unix"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/disk"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/omni"
)

// TestSandbox_Disk proves the disk module end to end (spec 008 NFR-005): the
// schema is applied, the daemon collects every second and publishes every two,
// the metrics are read back as series and the size as a dimension. The test
// takes its own statfs of / as the truth for the size, and for whether the
// volume has an inode limit at all (US-4/2: this dev host is btrfs).
func TestSandbox_Disk(t *testing.T) {
	s, err := config.Load("", os.Getenv)
	if err != nil || s.RequireAPI() != nil {
		t.Skip("sandbox credentials not set")
	}
	var st unix.Statfs_t
	if err := unix.Statfs("/", &st); err != nil {
		t.Skipf("needs statfs(/): %v", err)
	}
	wantTotal := floorGiB2(uint64(st.Blocks) * uint64(st.Bsize))
	hasInodes := st.Files > 0

	reg := module.NewRegistry()
	reg.Register(machineid.New(), module.Required())
	reg.Register(disk.New())
	app := &cli.App{Registry: reg, Version: "sandbox"}
	env := func(k string) string {
		if k == config.EnvIdentity {
			return "sandbox-disk"
		}
		return os.Getenv(k)
	}

	cfg := t.TempDir() + "/omnistat.yaml"
	if err := os.WriteFile(cfg, []byte("modules:\n  disk:\n    interval: 1s\npublish:\n  interval: 2s\n"), 0o600); err != nil {
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
	logs := errb.String()

	// US-4/2: with no inode limit, one notice for the whole run and no
	// omission record from any of its seven-odd collections.
	notices := strings.Count(logs, "no inode limit")
	if !hasInodes && notices != 1 {
		t.Errorf("btrfs-style volume: want exactly one inode notice, got %d:\n%s", notices, logs)
	}
	if hasInodes && notices != 0 {
		t.Errorf("volume has inodes, yet the notice was logged:\n%s", logs)
	}
	if strings.Contains(logs, "observations omitted") {
		t.Errorf("a healthy host omits nothing:\n%s", logs)
	}

	api, err := omni.New(omni.Settings{BaseURL: s.BaseURL, Token: s.Token, ProjectID: s.ProjectID, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := api.ReadSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{
		"disk_root_used_pct": "metric", "disk_root_available_gib": "metric", "disk_root_total_gib": "number",
		"disk_root_inodes_used_pct": "metric", "disk_read_mibps": "metric", "disk_write_mibps": "metric",
		"disk_read_iops": "metric", "disk_write_iops": "metric", "disk_busy_pct": "metric",
	}
	for slug, typ := range kinds {
		a, ok := cur.Attributes[slug]
		if !ok {
			t.Fatalf("%s was not created", slug)
		}
		if a.Type != typ {
			t.Fatalf("%s must be %s, got %q", slug, typ, a.Type)
		}
	}
	found := eventually(t, "find sandbox-disk", func(ctx context.Context) ([]identity.EntitySummary, error) {
		return api.FindEntities(ctx, cur.Templates["host"].ID, "machine_id", "sandbox-disk")
	}, func(es []identity.EntitySummary) bool { return len(es) > 0 })
	host := found[0].ID

	// US-2/1: the size, read back from the entity.
	vals := eventually(t, "disk_root_total_gib read back", func(ctx context.Context) (map[string]string, error) {
		return api.EntityValues(ctx, host, "disk_root_total_gib")
	}, func(v map[string]string) bool { return v["disk_root_total_gib"] != "" })
	gotTotal, err := strconv.ParseFloat(vals["disk_root_total_gib"], 64)
	if err != nil || gotTotal != wantTotal {
		t.Fatalf("disk_root_total_gib = %q, want %v (from statfs /)", vals["disk_root_total_gib"], wantTotal)
	}

	// US-1/1, US-3/4: every series the host can report, with its own times.
	pctOK := func(v float64) bool { return v >= 0 && v <= 100 && twoDecimals32(v) }
	rateOK := func(v float64) bool { return v >= 0 && twoDecimals32(v) }
	checks := map[string]func(float64) bool{
		"disk_root_used_pct":      pctOK,
		"disk_root_available_gib": func(v float64) bool { return v > 0 && v <= wantTotal && twoDecimals32(v) },
		"disk_read_mibps":         rateOK,
		"disk_write_mibps":        rateOK,
		"disk_read_iops":          rateOK,
		"disk_write_iops":         rateOK,
		"disk_busy_pct":           pctOK,
	}
	if hasInodes {
		checks["disk_root_inodes_used_pct"] = pctOK
	}
	ids := make([]string, 0, len(checks))
	slugOf := map[string]string{}
	for slug := range checks {
		id := cur.Attributes[slug].ID
		ids = append(ids, id)
		slugOf[id] = slug
	}
	series := eventually(t, "disk series", func(ctx context.Context) (map[string][]omni.ChartPoint, error) {
		return api.EntityChart(ctx, host, ids, started, time.Now().Add(time.Minute), "1 second", "last")
	}, func(m map[string][]omni.ChartPoint) bool {
		for _, id := range ids {
			if distinct(m[id]) < 2 {
				return false
			}
		}
		return true
	})
	for _, id := range ids {
		slug, points := slugOf[id], series[id]
		for _, p := range points {
			if !checks[slug](p.Value) {
				t.Errorf("implausible value %v at %v for %s", p.Value, p.At, slug)
			}
		}
		t.Logf("%s: %d points, %d distinct timestamps, first=%v last=%v", slug, len(points), distinct(points), points[0], points[len(points)-1])
	}

	// US-4/2: no inode series at all where the volume has no inode limit.
	if !hasInodes {
		id := cur.Attributes["disk_root_inodes_used_pct"].ID
		inodes, err := api.EntityChart(context.Background(), host, []string{id}, started, time.Now().Add(time.Minute), "1 second", "last")
		if err != nil {
			t.Fatal(err)
		}
		if len(inodes[id]) != 0 {
			t.Fatalf("no inode value may be published without an inode limit, got %+v", inodes[id])
		}
	}
	t.Logf("disk_root_total_gib read back as %q (statfs: %v)", vals["disk_root_total_gib"], wantTotal)
}

// twoDecimals32 reports whether v, as the chart returns it through float32, is
// a value with at most two decimal places. twoDecimals' fixed tolerance is
// too tight once values reach the hundreds (365.73 comes back as
// 365.7300109…), so compare in float32 instead: exact at any magnitude.
func twoDecimals32(v float64) bool {
	return float32(v) == float32(math.Round(v*100)/100)
}

// floorGiB2 is spec 008 FR-008's rule, restated here so the test does not
// trust the code under test: GiB, rounded down to two decimal places.
func floorGiB2(b uint64) float64 {
	const gib = 1 << 30
	return float64((b/gib)*100+(b%gib)*100/gib) / 100
}
