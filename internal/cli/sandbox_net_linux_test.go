//go:build sandbox && linux

package cli_test

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/module/network"
	"github.com/omnismith-apps/omnistat/internal/omni"
)

// TestSandbox_Net proves the net module end to end (spec 010 NFR-005): the
// schema is applied, the daemon collects every second and publishes every
// two, and every series this host can report is read back. The test looks at
// /sys and /proc itself to decide what the host can report: whether it has a
// device-backed interface (FR-009) and whether connection tracking is loaded
// (FR-014).
func TestSandbox_Net(t *testing.T) {
	s, err := config.Load("", os.Getenv)
	if err != nil || s.RequireAPI() != nil {
		t.Skip("sandbox credentials not set")
	}
	devices, _ := filepath.Glob("/sys/class/net/*/device")
	hasNIC := len(devices) > 0
	_, err = os.Stat("/proc/sys/net/netfilter/nf_conntrack_max")
	hasConntrack := err == nil

	reg := module.NewRegistry()
	reg.Register(machineid.New(), module.Required())
	reg.Register(network.New())
	app := &cli.App{Registry: reg, Version: "sandbox"}
	env := func(k string) string {
		if k == config.EnvIdentity {
			return "sandbox-net"
		}
		return os.Getenv(k)
	}

	cfg := t.TempDir() + "/omnistat.yaml"
	if err := os.WriteFile(cfg, []byte("modules:\n  net:\n    interval: 1s\npublish:\n  interval: 2s\n"), 0o600); err != nil {
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
	if strings.Contains(logs, "observations omitted") {
		t.Errorf("a healthy host omits nothing:\n%s", logs)
	}
	// FR-009, FR-014: the steady states are said once per run, or not at all.
	for notice, expected := range map[string]bool{
		"no physical network interface":  !hasNIC,
		"connection tracking not loaded": !hasConntrack,
	} {
		want := 0
		if expected {
			want = 1
		}
		if n := strings.Count(logs, notice); n != want {
			t.Errorf("%q logged %d times, want %d:\n%s", notice, n, want, logs)
		}
	}

	api, err := omni.New(omni.Settings{BaseURL: s.BaseURL, Token: s.Token, ProjectID: s.ProjectID, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := api.ReadSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	all := []string{
		"net_rx_mbps", "net_tx_mbps", "net_rx_pps", "net_tx_pps",
		"net_rx_errors_ps", "net_tx_errors_ps", "net_rx_drops_ps", "net_tx_drops_ps",
		"net_tcp_established", "net_tcp_retrans_pct", "net_tcp_resets_ps",
		"net_tcp_listen_drops_ps", "net_tcp_time_wait", "net_udp_errors_ps", "net_conntrack_used_pct",
	}
	for _, slug := range all {
		a, ok := cur.Attributes[slug]
		if !ok {
			t.Fatalf("%s was not created", slug)
		}
		if a.Type != "metric" {
			t.Fatalf("%s must be a metric, got %q", slug, a.Type)
		}
	}
	found := eventually(t, "find sandbox-net", func(ctx context.Context) ([]identity.EntitySummary, error) {
		return api.FindEntities(ctx, cur.Templates["host"].ID, "machine_id", "sandbox-net")
	}, func(es []identity.EntitySummary) bool { return len(es) > 0 })
	host := found[0].ID

	// US-1/1, US-3, US-4: every series the host can report, with its own
	// times. The test's own API traffic means TCP segments are sent, so the
	// retransmission share has windows to report.
	pctOK := func(v float64) bool { return v >= 0 && v <= 100 && twoDecimals32(v) }
	rateOK := func(v float64) bool { return v >= 0 && twoDecimals32(v) }
	countOK := func(v float64) bool { return v >= 0 && v == math.Trunc(v) }
	checks := map[string]func(float64) bool{
		"net_tcp_established":     countOK,
		"net_tcp_retrans_pct":     pctOK,
		"net_tcp_resets_ps":       rateOK,
		"net_tcp_listen_drops_ps": rateOK,
		"net_tcp_time_wait":       countOK,
		"net_udp_errors_ps":       rateOK,
	}
	if hasNIC {
		for _, slug := range all[:8] {
			checks[slug] = rateOK
		}
	}
	if hasConntrack {
		checks["net_conntrack_used_pct"] = pctOK
	}
	ids := make([]string, 0, len(checks))
	slugOf := map[string]string{}
	for slug := range checks {
		id := cur.Attributes[slug].ID
		ids = append(ids, id)
		slugOf[id] = slug
	}
	series := eventually(t, "net series", func(ctx context.Context) (map[string][]omni.ChartPoint, error) {
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
	t.Logf("device-backed interfaces: %v; conntrack loaded: %v", devices, hasConntrack)
}
