// Package network is the `net` module (spec 010): how much traffic the host's
// physical interfaces move, and whether its TCP/IP stack is healthy. It
// publishes interface throughput, packet, error and drop rates; TCP
// established connections, retransmission share, resets, listen drops and
// TIME_WAIT sockets; UDP receive errors; and connection-tracking table use.
// Everything is a metric on the host entity.
//
// The package is named network so that it does not shadow the standard
// library's net; the module's name is "net".
//
// The rates are differences between cumulative counters, so, like cpu and
// disk, the module keeps its previous reading and primes itself on the first
// collection (ADR-0006). What an OS does not maintain is gated per attribute
// (ADR-0007): three values are Linux-only, and macOS collects nothing.
package network

import (
	"context"
	"log/slog"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// Name is the module name.
const Name = "net"

// Manifest keys (spec 010 FR-001). The operator overrides slugs, never keys.
const (
	KeyRxMbps           = "rx_mbps"
	KeyTxMbps           = "tx_mbps"
	KeyRxPps            = "rx_pps"
	KeyTxPps            = "tx_pps"
	KeyRxErrorsPs       = "rx_errors_ps"
	KeyTxErrorsPs       = "tx_errors_ps"
	KeyRxDropsPs        = "rx_drops_ps"
	KeyTxDropsPs        = "tx_drops_ps"
	KeyTCPEstablished   = "tcp_established"
	KeyTCPRetransPct    = "tcp_retrans_pct"
	KeyTCPResetsPs      = "tcp_resets_ps"
	KeyTCPListenDropsPs = "tcp_listen_drops_ps"
	KeyTCPTimeWait      = "tcp_time_wait"
	KeyUDPErrorsPs      = "udp_errors_ps"
	KeyConntrackUsedPct = "conntrack_used_pct"
)

// DefaultInterval is how often the module is collected unless overridden
// (FR-004): the rates become 30-second averages, two points per default publish.
const DefaultInterval = 30 * time.Second

// DefaultPrime is how long the first collection waits between its two counter
// readings so that a one-shot `omnistat run` publishes real rates (FR-015).
const DefaultPrime = 250 * time.Millisecond

// primeSlack is the headroom required on top of the prime before the module
// is willing to spend the deadline priming (FR-015).
const primeSlack = 50 * time.Millisecond

// Where each attribute is collectable (FR-018). macOS is in neither list: the
// only interface source there runs an external command, and its TCP
// statistics are unverifiable here. Windows keeps no listen-drop or TIME_WAIT
// counter and has no connection tracking.
var (
	linuxWindows = []string{"linux", "windows"}
	linuxOnly    = []string{"linux"}
)

// Module is the net module. Reader, GOOS, Now and Sleep are injectable so
// that every requirement is testable without a real network, platform or
// pause (NFR-004).
type Module struct {
	Reader Reader
	GOOS   string
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
	// Prime is the first collection's sampling window; zero means DefaultPrime.
	Prime time.Duration
	// Log receives the omission record, the two notices and the debug record;
	// nil means the default logger.
	Log *slog.Logger

	mu sync.Mutex
	// last is the previous reading of the rate areas, held for the lifetime of
	// the process and never persisted (FR-015).
	last *reading
	// noIface and noConntrack make the steady-state notices once per process
	// (FR-009, FR-014).
	noIface     sync.Once
	noConntrack sync.Once
}

// New returns the module bound to the real host.
func New() *Module {
	return &Module{
		Reader: hostread.Net{},
		GOOS:   runtime.GOOS,
		Now:    time.Now,
		Sleep:  sleep,
		Prime:  DefaultPrime,
	}
}

// sleep waits for d or until ctx is done, whichever comes first.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Name implements module.Module.
func (m *Module) Name() string { return Name }

// DefaultInterval implements module.Provider (FR-004).
func (m *Module) DefaultInterval() time.Duration { return DefaultInterval }

// Manifest implements module.Module (FR-001, FR-018). Every attribute is a
// metric on the host template.
func (m *Module) Manifest() manifest.Manifest {
	metric := func(key, slug, name, desc string, platforms []string) manifest.Attribute {
		return manifest.Attribute{Key: key, Slug: slug, Name: name, Kind: manifest.KindMetric, Description: desc, Platforms: platforms}
	}
	return manifest.Manifest{
		Module: Name,
		Attributes: []manifest.Attribute{
			metric(KeyRxMbps, "net_rx_mbps", "Network receive throughput",
				"Bits received by the physical interfaces per second, in Mbit/s (10^6 bits)", linuxWindows),
			metric(KeyTxMbps, "net_tx_mbps", "Network transmit throughput",
				"Bits sent by the physical interfaces per second, in Mbit/s (10^6 bits)", linuxWindows),
			metric(KeyRxPps, "net_rx_pps", "Packets received",
				"Packets received by the physical interfaces per second", linuxWindows),
			metric(KeyTxPps, "net_tx_pps", "Packets sent",
				"Packets sent by the physical interfaces per second", linuxWindows),
			metric(KeyRxErrorsPs, "net_rx_errors_ps", "Receive errors",
				"Received packets the physical interfaces found faulty, per second", linuxWindows),
			metric(KeyTxErrorsPs, "net_tx_errors_ps", "Transmit errors",
				"Packets the physical interfaces failed to send, per second", linuxWindows),
			metric(KeyRxDropsPs, "net_rx_drops_ps", "Receive drops",
				"Received packets discarded before reaching the network stack, per second", linuxWindows),
			metric(KeyTxDropsPs, "net_tx_drops_ps", "Transmit drops",
				"Outgoing packets discarded before transmission, per second", linuxWindows),
			metric(KeyTCPEstablished, "net_tcp_established", "TCP connections established",
				"TCP connections currently established, as the OS's TCP MIB counts them", linuxWindows),
			metric(KeyTCPRetransPct, "net_tcp_retrans_pct", "TCP retransmitted",
				"Percent of the TCP segments sent that were retransmissions", linuxWindows),
			metric(KeyTCPResetsPs, "net_tcp_resets_ps", "TCP resets sent",
				"TCP segments sent with the reset flag, per second", linuxWindows),
			metric(KeyTCPListenDropsPs, "net_tcp_listen_drops_ps", "TCP listen drops",
				"Incoming connections dropped at listening sockets, per second", linuxOnly),
			metric(KeyTCPTimeWait, "net_tcp_time_wait", "TCP sockets in TIME_WAIT",
				"TCP sockets currently in TIME_WAIT", linuxOnly),
			metric(KeyUDPErrorsPs, "net_udp_errors_ps", "UDP receive errors",
				"Received UDP datagrams that could not be delivered to an application for a reason other than no listener, per second", linuxWindows),
			metric(KeyConntrackUsedPct, "net_conntrack_used_pct", "Connection tracking used",
				"Percent of the connection-tracking table's limit in use", linuxOnly),
		},
	}
}

// Collect implements module.Provider (spec 010 FR-005…FR-024).
//
// Each area is read independently, so any of them can fail and cost only what
// depends on it (FR-019). Omissions are reported in one record at the end
// (FR-022); only a collection that produced nothing while something failed is
// a provider failure (FR-020). Steady states (no physical interface, no
// connection tracking) are logged once and are not omissions (FR-021).
func (m *Module) Collect(ctx context.Context) ([]module.Observation, error) {
	var om module.Omissions
	linux := manifest.Collectable(linuxOnly, m.goos())

	obs, cur := m.rates(ctx, linux, &om)
	obs = append(obs, m.gauges(ctx, cur, linux, &om)...)

	// FR-024: what was measured, for an operator checking the numbers. The
	// values themselves are logged by the core at debug (003 FR-026).
	m.logger().Debug("net read", "module", Name, "interfaces", strings.Join(cur.names(), ","))

	if len(obs) == 0 && om.Len() > 0 {
		return nil, om.Err(Name)
	}
	om.Log(m.logger(), Name)
	return obs, nil
}

// addAll records err as the reason for every key; a nil err records nothing.
func addAll(om *module.Omissions, err error, keys ...string) {
	if err == nil {
		return
	}
	for _, k := range keys {
		om.Add(k, err)
	}
}

// round2 rounds to two decimal places, half away from zero (FR-006…FR-014).
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// pct is part ÷ whole × 100, clamped to [0, 100] and rounded to two decimal
// places (FR-012, FR-014). The caller guarantees whole > 0.
func pct(part, whole float64) float64 {
	return round2(min(max(part/whole*100, 0), 100))
}

func (m *Module) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Module) goos() string {
	if m.GOOS != "" {
		return m.GOOS
	}
	return runtime.GOOS
}

func (m *Module) logger() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}
