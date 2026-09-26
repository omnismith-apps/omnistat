package network_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/network"
)

// FR-001, FR-003, FR-018: the manifest is the module's contract with the
// project schema, pinned attribute by attribute. Changing a default slug is a
// breaking change that needs an ADR (001 FR-002).
func TestManifest_DefaultSlugs(t *testing.T) {
	m := network.New().Manifest()
	if m.Module != "net" {
		t.Fatalf("module name: %q", m.Module)
	}
	if len(m.Templates) != 0 {
		t.Fatalf("net attaches to the host template, declaring none of its own: %+v", m.Templates)
	}
	const both, linux = "linux,windows", "linux"
	want := []struct {
		key, slug, name, platforms string
	}{
		{"rx_mbps", "net_rx_mbps", "Network receive throughput", both},
		{"tx_mbps", "net_tx_mbps", "Network transmit throughput", both},
		{"rx_pps", "net_rx_pps", "Packets received", both},
		{"tx_pps", "net_tx_pps", "Packets sent", both},
		{"rx_errors_ps", "net_rx_errors_ps", "Receive errors", both},
		{"tx_errors_ps", "net_tx_errors_ps", "Transmit errors", both},
		{"rx_drops_ps", "net_rx_drops_ps", "Receive drops", both},
		{"tx_drops_ps", "net_tx_drops_ps", "Transmit drops", both},
		{"tcp_established", "net_tcp_established", "TCP connections established", both},
		{"tcp_retrans_pct", "net_tcp_retrans_pct", "TCP retransmitted", both},
		{"tcp_resets_ps", "net_tcp_resets_ps", "TCP resets sent", both},
		{"tcp_listen_drops_ps", "net_tcp_listen_drops_ps", "TCP listen drops", linux},
		{"tcp_time_wait", "net_tcp_time_wait", "TCP sockets in TIME_WAIT", linux},
		{"udp_errors_ps", "net_udp_errors_ps", "UDP receive errors", both},
		{"conntrack_used_pct", "net_conntrack_used_pct", "Connection tracking used", linux},
	}
	if len(m.Attributes) != len(want) {
		t.Fatalf("got %d attributes, want %d: %+v", len(m.Attributes), len(want), m.Attributes)
	}
	for i, w := range want {
		a := m.Attributes[i]
		if a.Key != w.key || a.Slug != w.slug || a.Name != w.name || a.Kind != manifest.KindMetric {
			t.Errorf("attribute %d: got %+v, want key=%s slug=%s name=%s kind=metric", i, a, w.key, w.slug, w.name)
		}
		if got := strings.Join(a.Platforms, ","); got != w.platforms {
			t.Errorf("%s platforms: got %q, want %q", w.key, got, w.platforms)
		}
		if len(a.Options) != 0 || a.Template != "" {
			t.Errorf("%s: no options and the host template expected: %+v", w.key, a)
		}
		if strings.TrimSpace(a.Description) == "" {
			t.Errorf("%s: description is empty", w.key)
		}
	}
	if err := manifest.Validate([]manifest.Manifest{m}); err != nil {
		t.Fatalf("manifest must validate: %v", err)
	}
}

// FR-004.
func TestDefaultInterval(t *testing.T) {
	if got := network.New().DefaultInterval(); got != 30*time.Second {
		t.Fatalf("default interval %v, want 30s", got)
	}
}

// FR-002: enabled by default, switchable off.
func TestRegistry_EnabledByDefault(t *testing.T) {
	r := module.NewRegistry()
	r.Register(network.New())
	on, err := r.Enabled(nil)
	if err != nil || len(on) != 1 || on[0].Name() != "net" {
		t.Fatalf("enabled by default: %v %v", on, err)
	}
	off, err := r.Enabled(map[string]bool{"net": false})
	if err != nil || len(off) != 0 {
		t.Fatalf("disabled: %v %v", off, err)
	}
}

// FR-018, US-5/1-2: on Windows the three Linux-only attributes are skipped
// once at startup; on macOS nothing is collectable and the whole module is
// skipped, while the schema stays the same everywhere.
func TestSources_PlatformGating(t *testing.T) {
	m := network.New()
	mods := []module.Module{m}
	desired, err := manifest.Resolve(module.Manifests(mods), manifest.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(desired.Attributes) != 15 {
		t.Fatalf("the schema declares all 15 attributes everywhere, got %d", len(desired.Attributes))
	}

	src, skipped, err := collect.Sources(desired, mods, nil, "windows")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range skipped {
		keys = append(keys, s.Key)
	}
	sort.Strings(keys)
	if len(src) != 1 || len(src[0].Attrs) != 12 ||
		strings.Join(keys, ",") != "conntrack_used_pct,tcp_listen_drops_ps,tcp_time_wait" {
		t.Fatalf("windows: attrs=%d skipped=%v", len(src[0].Attrs), keys)
	}

	src, skipped, err = collect.Sources(desired, mods, nil, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if len(src) != 0 || len(skipped) != 1 || skipped[0].Module != "net" || skipped[0].Key != "" {
		t.Fatalf("darwin: sources=%v skipped=%+v", src, skipped)
	}
}

// FR-018: Windows never asks for what it does not maintain, and does not
// report those attributes as omissions.
func TestCollect_Windows_NoLinuxOnlyReads(t *testing.T) {
	r := host()
	r.listenErr, r.twErr, r.ctErr = errors.ErrUnsupported, errors.ErrUnsupported, errors.ErrUnsupported
	m, clk, buf := newTestModule(r, "windows")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	absent(t, got, network.KeyTCPListenDropsPs, network.KeyTCPTimeWait, network.KeyConntrackUsedPct)
	value(t, got, network.KeyRxMbps)
	value(t, got, network.KeyTCPEstablished)
	if _, _, listen, tw, ct := r.calls(); listen+tw+ct != 0 {
		t.Fatalf("Linux-only readers called on windows: listen=%d tw=%d ct=%d", listen, tw, ct)
	}
	if omissionRecords(buf) != 0 {
		t.Fatalf("no omission expected:\n%s", buf)
	}
}

// FR-019: the interface reading can fail alone.
func TestCollect_InterfacesFail_StackStillPublished(t *testing.T) {
	r := host()
	r.ifacesErr = errRead
	m, clk, buf := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	absent(t, got, ifaceKeys...)
	for _, k := range []string{network.KeyTCPEstablished, network.KeyTCPResetsPs, network.KeyTCPTimeWait, network.KeyConntrackUsedPct} {
		value(t, got, k)
	}
	if !strings.Contains(buf.String(), "keys=rx_mbps,tx_mbps,rx_pps,tx_pps,rx_errors_ps,tx_errors_ps,rx_drops_ps,tx_drops_ps") {
		t.Fatalf("interface keys must be reported omitted:\n%s", buf)
	}
}

// FR-019: the TCP/UDP reading can fail alone.
func TestCollect_StackFails_InterfacesStillPublished(t *testing.T) {
	r := host()
	r.stackErr = errRead
	m, clk, buf := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	absent(t, got, network.KeyTCPEstablished, network.KeyTCPRetransPct, network.KeyTCPResetsPs, network.KeyUDPErrorsPs)
	value(t, got, network.KeyRxMbps)
	value(t, got, network.KeyTCPListenDropsPs)
	if omissionRecords(buf) != 2 { // one per collection
		t.Fatalf("want one omission record per collection:\n%s", buf)
	}
	if !strings.Contains(buf.String(), "tcp_established,tcp_retrans_pct,tcp_resets_ps,udp_errors_ps") {
		t.Fatalf("stack keys must be reported omitted:\n%s", buf)
	}
}

// FR-020: nothing readable at all is an ordinary provider failure.
func TestCollect_AllFail_ProviderError(t *testing.T) {
	r := host()
	r.ifacesErr, r.stackErr, r.listenErr, r.twErr, r.ctErr = errRead, errRead, errRead, errRead, errRead
	m, _, _ := newTestModule(r, "linux")
	obs, err := m.Collect(context.Background())
	if err == nil || len(obs) != 0 {
		t.Fatalf("want a provider error and nothing observed, got %v, %v", obs, err)
	}
	if !strings.Contains(err.Error(), "net: nothing could be read") {
		t.Fatalf("error: %v", err)
	}
}

// FR-021, US-5: a container sees no physical interface and has no connection
// tracking. With the TCP values readable, that is a complete collection: no
// error, no omission, and one notice for each steady state.
func TestCollect_ContainerIsComplete(t *testing.T) {
	r := host()
	r.ifaces = [][]hostread.IfaceCounters{ifs(sw("eth0", 5, 5))} // a veth, seen from inside
	r.ctErr = hostread.ErrNoConntrack
	m, clk, buf := newTestModule(r, "linux")
	for range 3 {
		collectOK(t, m)
		clk.Advance(30 * time.Second)
	}
	if omissionRecords(buf) != 0 {
		t.Fatalf("no omission expected:\n%s", buf)
	}
	for _, notice := range []string{"no physical network interface", "connection tracking not loaded"} {
		if n := strings.Count(buf.String(), notice); n != 1 {
			t.Errorf("%q logged %d times, want once:\n%s", notice, n, buf)
		}
	}
}

// FR-022: every failed area goes into one record per collection.
func TestCollect_OneOmissionRecord(t *testing.T) {
	r := host()
	r.ifacesErr = errRead
	r.twErr = errRead
	m, _, buf := newTestModule(r, "linux")
	collectOK(t, m)
	if omissionRecords(buf) != 1 {
		t.Fatalf("want exactly one omission record:\n%s", buf)
	}
	rec := buf.String()
	for _, k := range []string{"rx_mbps", "tcp_time_wait"} {
		if !strings.Contains(rec, k) {
			t.Errorf("record does not name %s:\n%s", k, rec)
		}
	}
}

// FR-017: one reading per area per collection; the first collection takes a
// second reading of each rate area to prime (FR-015).
func TestCollect_OneReadPerArea(t *testing.T) {
	r := host()
	m, clk, _ := newTestModule(r, "linux")
	collectOK(t, m)
	if i, s, l, tw, ct := r.calls(); i != 2 || s != 2 || l != 2 || tw != 1 || ct != 1 {
		t.Fatalf("priming collect: ifaces=%d stack=%d listen=%d tw=%d ct=%d", i, s, l, tw, ct)
	}
	clk.Advance(30 * time.Second)
	collectOK(t, m)
	if i, s, l, tw, ct := r.calls(); i != 3 || s != 3 || l != 3 || tw != 2 || ct != 2 {
		t.Fatalf("steady collect: ifaces=%d stack=%d listen=%d tw=%d ct=%d", i, s, l, tw, ct)
	}
}

// FR-024: the debug record names the counted interfaces, so an operator can
// check what was measured.
func TestCollect_DebugRecordNamesInterfaces(t *testing.T) {
	r := host()
	r.ifaces = [][]hostread.IfaceCounters{ifs(nic("eth1", 0, 0), sw("docker0", 0, 0), nic("eth0", 0, 0))}
	m, _, buf := newTestModule(r, "linux")
	collectOK(t, m)
	if !strings.Contains(buf.String(), `msg="net read" module=net interfaces=eth0,eth1`) {
		t.Fatalf("debug record missing or wrong:\n%s", buf)
	}
}
