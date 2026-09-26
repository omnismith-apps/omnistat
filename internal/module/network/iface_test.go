package network_test

import (
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module/network"
)

// FR-006: throughput is bytes × 8 over the elapsed seconds, in decimal
// megabits (10⁶), rounded to two decimal places.
func TestRates_Mbps(t *testing.T) {
	r := host()
	steadyIfaces(r,
		ifs(nic("eth0", 1000, 2000)),
		ifs(nic("eth0", 1000+375_000_000, 2000+1_000_001)),
	)
	m, clk, buf := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	for key, want := range map[string]float64{
		network.KeyRxMbps: 100,  // 375 MB × 8 / 30 s = 100 Mbit/s (not 95.37: decimal, not MiB)
		network.KeyTxMbps: 0.27, // 1 000 001 B × 8 / 30 s = 266 666.9 bit/s
	} {
		if v := value(t, got, key); v != want {
			t.Errorf("%s = %v, want %v", key, v, want)
		}
	}
	if omissionRecords(buf) != 0 {
		t.Fatalf("unexpected omissions:\n%s", buf)
	}
}

// FR-007, FR-008, US-2/1: packets, errors and drops per second, each in its
// own direction.
func TestRates_PacketsErrorsDrops(t *testing.T) {
	base := hostread.IfaceCounters{ID: "eth0", Name: "eth0", Physical: true,
		RxPackets: 100, TxPackets: 200, RxErrors: 1, TxErrors: 2, RxDrops: 3, TxDrops: 4}
	later := base
	later.RxPackets += 3000 // 100/s
	later.TxPackets += 1501 // 50.03/s
	later.RxErrors += 30    // 1/s
	later.RxDrops += 3      // 0.1/s
	r := host()
	steadyIfaces(r, ifs(base), ifs(later))
	m, clk, _ := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	for key, want := range map[string]float64{
		network.KeyRxPps:      100,
		network.KeyTxPps:      50.03,
		network.KeyRxErrorsPs: 1,
		network.KeyTxErrorsPs: 0, // the transmit side is unaffected
		network.KeyRxDropsPs:  0.1,
		network.KeyTxDropsPs:  0,
	} {
		if v := value(t, got, key); v != want {
			t.Errorf("%s = %v, want %v", key, v, want)
		}
	}
}

// US-2/2: an idle, healthy NIC publishes zeros; zero is a value, not an
// absence.
func TestRates_IdleIsZeroNotAbsent(t *testing.T) {
	r := host()
	m, clk, _ := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	for _, k := range ifaceKeys {
		if v := value(t, got, k); v != 0 {
			t.Errorf("%s = %v, want 0", k, v)
		}
	}
}

// FR-005, US-1/3: a container's download crosses the NIC, the bridge and the
// veth. Only the NIC is counted, so 1 GB is counted once. Two NICs are
// summed.
func TestRates_ContainerDownload_CountedOnce(t *testing.T) {
	const gb = 1_000_000_000
	r := host()
	steadyIfaces(r,
		ifs(nic("eth0", 0, 0), nic("eth1", 0, 0), sw("docker0", 0, 0), sw("veth1", 0, 0), sw("lo", 0, 0), sw("wg0", 0, 0)),
		ifs(nic("eth0", gb, 0), nic("eth1", gb/2, 0), sw("docker0", gb, 0), sw("veth1", 0, gb), sw("lo", gb, gb), sw("wg0", gb, 0)),
	)
	m, clk, _ := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	// 1.5 GB × 8 / 30 s = 400 Mbit/s; any software interface would add to it.
	if v := value(t, got, network.KeyRxMbps); v != 400 {
		t.Fatalf("rx = %v Mbit/s, want 400 (eth0 + eth1 only)", v)
	}
	if v := value(t, got, network.KeyTxMbps); v != 0 {
		t.Fatalf("tx = %v Mbit/s, want 0", v)
	}
}

// FR-009, FR-021: no physical interface (a container's veth, seen from
// inside) publishes no interface value, not zeros; it is logged once and is
// not an omission.
func TestIface_NoPhysical_NoticeOnceNoOmission(t *testing.T) {
	r := host()
	r.ifaces = [][]hostread.IfaceCounters{ifs(sw("eth0", 0, 0), sw("lo", 0, 0))}
	m, clk, buf := newTestModule(r, "linux")
	for range 2 {
		got := collectOK(t, m)
		absent(t, got, ifaceKeys...)
		value(t, got, network.KeyTCPEstablished)
		clk.Advance(30 * time.Second)
	}
	if n := strings.Count(buf.String(), "no physical network interface"); n != 1 {
		t.Fatalf("notice logged %d times, want once:\n%s", n, buf)
	}
	if strings.Count(buf.String(), "level=INFO") != 1 || omissionRecords(buf) != 0 {
		t.Fatalf("one info notice, no omission expected:\n%s", buf)
	}
}

// FR-015: an interface present in only one reading contributes nothing; its
// lifetime counters never appear as a spike. A rename is the same thing.
func TestIface_HotplugContributesNothing(t *testing.T) {
	r := host()
	steadyIfaces(r,
		ifs(nic("eth0", 0, 0)),
		ifs(nic("eth0", 30_000_000, 0), nic("usb0", 9_000_000_000, 0)), // usb0 plugged in
		ifs(nic("usb0", 9_030_000_000, 0)),                             // eth0 removed
		ifs(nic("wan0", 5, 0)),                                         // usb0 renamed
	)
	m, clk, buf := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	if v := value(t, collectOK(t, m), network.KeyRxMbps); v != 8 { // 30 MB × 8 / 30 s
		t.Fatalf("hot-plugged interface spiked the rate: %v", v)
	}
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), network.KeyRxMbps); v != 8 { // usb0: 30 MB over 30 s
		t.Fatalf("after removal: %v, want 8", v)
	}
	clk.Advance(30 * time.Second)
	absent(t, collectOK(t, m), ifaceKeys...) // nothing in common: nothing to say
	if omissionRecords(buf) != 0 || strings.Contains(buf.String(), "no physical network interface") {
		t.Fatalf("a rename is neither an omission nor 'no interface':\n%s", buf)
	}
}

// FR-015: a counter that went backwards on any interface (driver reload)
// drops only the quantities that depend on it for that collection, and the
// new reading is the baseline.
func TestIface_Regression_DropsOnlyThatQuantity(t *testing.T) {
	a := hostread.IfaceCounters{ID: "eth0", Name: "eth0", Physical: true, RxBytes: 1_000_000, TxBytes: 1_000_000, RxPackets: 100}
	b := a
	b.RxBytes = 10 // reset
	b.TxBytes += 3_750_000
	c := b
	c.RxBytes += 3_750_000
	r := host()
	steadyIfaces(r, ifs(a), ifs(b), ifs(c))
	m, clk, _ := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	absent(t, got, network.KeyRxMbps)
	if v := value(t, got, network.KeyTxMbps); v != 1 {
		t.Fatalf("tx = %v, want 1 (unaffected by rx's reset)", v)
	}
	value(t, got, network.KeyRxPps)
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), network.KeyRxMbps); v != 1 {
		t.Fatalf("rx measures from the reset reading next time: got %v, want 1", v)
	}
}
