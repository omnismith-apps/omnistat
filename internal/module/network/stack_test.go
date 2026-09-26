package network_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module/network"
)

// FR-011, FR-016: the gauges come from the current reading, as whole numbers,
// on the very first collection too, even one with no time to prime.
func TestGauges_FirstCollect(t *testing.T) {
	r := host()
	r.stacks = []hostread.StackCounters{stack(40, 0, 0, 0, 0)}
	r.tw = 7
	m, clk, _ := newTestModule(r, "linux")
	ctx, cancel := context.WithDeadline(context.Background(), clk.Now().Add(10*time.Millisecond))
	defer cancel()
	obs, err := m.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(t, obs)
	absent(t, got, rateKeys...)
	for key, want := range map[string]float64{
		network.KeyTCPEstablished:   40,
		network.KeyTCPTimeWait:      7,
		network.KeyConntrackUsedPct: 10, // 100 / 1000
	} {
		if v := value(t, got, key); v != want {
			t.Errorf("%s = %v, want %v", key, v, want)
		}
	}
}

// FR-012, US-3/2: the share counts retransmissions among all segments sent;
// neither OS counts them in OutSegs (feature 010 spike).
func TestRetransPct(t *testing.T) {
	r := host()
	steadyStack(r, stack(40, 5000, 100, 0, 0), stack(40, 5000+9950, 100+50, 0, 0))
	m, clk, _ := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	if v := value(t, collectOK(t, m), network.KeyTCPRetransPct); v != 0.5 {
		t.Fatalf("retrans = %v%%, want 0.5 (50 of 10 000)", v)
	}
}

// FR-012: the denominator is every segment sent, retransmissions included;
// all retransmissions is 100%, never more; rounding is to two decimal places.
func TestRetransPct_ClampAndRound(t *testing.T) {
	for _, c := range []struct {
		out, retrans uint64
		want         float64
	}{
		{0, 7, 100},
		{900, 100, 10}, // 100 of 1000 sent, not 100 per 900 (11.11)
		{2, 1, 33.33},
		{1, 2, 66.67},
	} {
		r := host()
		steadyStack(r, stack(1, 0, 0, 0, 0), stack(1, c.out, c.retrans, 0, 0))
		m, clk, _ := newTestModule(r, "linux")
		primeThenAdvance(t, m, clk)
		if v := value(t, collectOK(t, m), network.KeyTCPRetransPct); v != c.want {
			t.Errorf("out=%d retrans=%d: %v, want %v", c.out, c.retrans, v, c.want)
		}
	}
}

// FR-012, US-3/4: no segment sent in the window is no share at all, and not
// an omission.
func TestRetransPct_NoSegments_NoValueNoOmission(t *testing.T) {
	r := host()
	m, clk, buf := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	absent(t, got, network.KeyTCPRetransPct)
	value(t, got, network.KeyTCPResetsPs)
	if omissionRecords(buf) != 0 {
		t.Fatalf("no omission expected:\n%s", buf)
	}
}

// FR-013, US-3/3, US-4/3: resets sent, UDP receive errors and listen drops
// are per-second rates.
func TestRates_ResetsUDPListenDrops(t *testing.T) {
	r := host()
	steadyStack(r, stack(1, 0, 0, 10, 20), stack(1, 0, 0, 10+60, 20+3))
	steadyListen(r, 5, 5+900)
	m, clk, _ := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	for key, want := range map[string]float64{
		network.KeyTCPResetsPs:      2,
		network.KeyUDPErrorsPs:      0.1,
		network.KeyTCPListenDropsPs: 30,
	} {
		if v := value(t, got, key); v != want {
			t.Errorf("%s = %v, want %v", key, v, want)
		}
	}
}

// FR-015: a Windows 32-bit retransmission counter wrapping drops the share
// for that collection only; resets are unaffected.
func TestStack_Regression_DropsOnlyThatQuantity(t *testing.T) {
	r := host()
	steadyStack(r,
		stack(1, 1000, 4_294_967_000, 10, 0),
		stack(1, 2000, 100, 40, 0), // retrans wrapped
		stack(1, 2990, 110, 40, 0),
	)
	m, clk, _ := newTestModule(r, "windows")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	absent(t, got, network.KeyTCPRetransPct)
	if v := value(t, got, network.KeyTCPResetsPs); v != 1 {
		t.Fatalf("resets = %v, want 1", v)
	}
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), network.KeyTCPRetransPct); v != 1 {
		t.Fatalf("share measures from the wrapped reading next time: got %v, want 1 (10 of 1000)", v)
	}
}

// FR-014, US-4/1: the table's use is entries over limit.
func TestConntrack_Pct(t *testing.T) {
	r := host()
	r.ct = hostread.Conntrack{Count: 196_000, Max: 262_144}
	m, _, _ := newTestModule(r, "linux")
	if v := value(t, collectOK(t, m), network.KeyConntrackUsedPct); v != 74.77 {
		t.Fatalf("conntrack = %v, want 74.77", v)
	}
}

// FR-014, US-4/2: not loaded, or a zero limit, publishes nothing, is logged
// once and is not an omission. Loading it later starts the values.
func TestConntrack_NotLoaded_NoticeOnceThenLoaded(t *testing.T) {
	for name, set := range map[string]func(*fakeReader){
		"not loaded": func(r *fakeReader) { r.ctErr = hostread.ErrNoConntrack },
		"max zero":   func(r *fakeReader) { r.ct = hostread.Conntrack{Count: 3, Max: 0} },
	} {
		t.Run(name, func(t *testing.T) {
			r := host()
			set(r)
			m, clk, buf := newTestModule(r, "linux")
			for range 3 {
				absent(t, collectOK(t, m), network.KeyConntrackUsedPct)
				clk.Advance(30 * time.Second)
			}
			if n := strings.Count(buf.String(), "connection tracking not loaded"); n != 1 || omissionRecords(buf) != 0 {
				t.Fatalf("want one notice and no omission, got %d notices:\n%s", n, buf)
			}
			r.mu.Lock()
			r.ctErr, r.ct = nil, hostread.Conntrack{Count: 50, Max: 100}
			r.mu.Unlock()
			if v := value(t, collectOK(t, m), network.KeyConntrackUsedPct); v != 50 {
				t.Fatalf("after loading: %v, want 50", v)
			}
		})
	}
}

// FR-019, FR-022: a conntrack file that exists but cannot be read is a real
// omission, unlike "not loaded".
func TestConntrack_ReadError_Omitted(t *testing.T) {
	r := host()
	r.ctErr = errRead
	m, _, buf := newTestModule(r, "linux")
	absent(t, collectOK(t, m), network.KeyConntrackUsedPct)
	if omissionRecords(buf) != 1 || !strings.Contains(buf.String(), "conntrack_used_pct") {
		t.Fatalf("want one omission naming conntrack:\n%s", buf)
	}
}

// FR-015, US-1/4: the first collection primes with a bounded 250ms pause and
// publishes real rates from that window, so a one-shot run has them.
func TestFirstCollectPrimes(t *testing.T) {
	r := host()
	r.ifaces = [][]hostread.IfaceCounters{ifs(nic("eth0", 0, 0)), ifs(nic("eth0", 3_125_000, 0))}
	r.stacks = []hostread.StackCounters{stack(1, 0, 0, 0, 0), stack(1, 99, 1, 5, 0)}
	r.listens = []uint64{0, 2}
	m, clk, _ := newTestModule(r, "linux")
	got := collectOK(t, m)
	if len(clk.sleeps) != 1 || clk.sleeps[0] != network.DefaultPrime || network.DefaultPrime != 250*time.Millisecond {
		t.Fatalf("want one 250ms prime, got %v", clk.sleeps)
	}
	for key, want := range map[string]float64{
		network.KeyRxMbps:           100, // 3.125 MB × 8 / 0.25 s
		network.KeyTCPRetransPct:    1,
		network.KeyTCPResetsPs:      20,
		network.KeyTCPListenDropsPs: 8,
	} {
		if v := value(t, got, key); v != want {
			t.Errorf("%s = %v, want %v", key, v, want)
		}
	}
	clk.Advance(30 * time.Second)
	collectOK(t, m)
	if len(clk.sleeps) != 1 {
		t.Fatalf("only the first collection primes, got %v", clk.sleeps)
	}
}

// FR-015, FR-022: a deadline too short for the prime yields no rate and no
// omission; that reading is the baseline for the next collection.
func TestTightDeadlineSkipsPrime(t *testing.T) {
	r := host()
	r.ifaces = [][]hostread.IfaceCounters{ifs(nic("eth0", 0, 0)), ifs(nic("eth0", 3_750_000, 0))}
	m, clk, buf := newTestModule(r, "linux")
	ctx, cancel := context.WithDeadline(context.Background(), clk.Now().Add(10*time.Millisecond))
	defer cancel()
	obs, err := m.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	absent(t, byKey(t, obs), rateKeys...)
	if len(clk.sleeps) != 0 || omissionRecords(buf) != 0 {
		t.Fatalf("no pause and no omission expected: sleeps=%v\n%s", clk.sleeps, buf)
	}
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), network.KeyRxMbps); v != 1 {
		t.Fatalf("second collect measures from the first reading: got %v, want 1", v)
	}
}

// FR-015: a pause cut short leaves the first reading as the baseline, never
// a mixture, and nothing is published for the window that was not measured.
func TestCancelledPauseKeepsConsistentBaseline(t *testing.T) {
	r := host()
	r.ifaces = [][]hostread.IfaceCounters{ifs(nic("eth0", 0, 0)), ifs(nic("eth0", 7_500_000, 0))}
	m, clk, buf := newTestModule(r, "linux")
	clk.sleepErr = context.Canceled
	got := collectOK(t, m)
	absent(t, got, rateKeys...)
	if i, _, _, _, _ := r.calls(); i != 1 || omissionRecords(buf) != 0 {
		t.Fatalf("the cut-short collect reads once and reports nothing: calls=%d\n%s", i, buf)
	}
	clk.sleepErr = nil
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), network.KeyRxMbps); v != 2 {
		t.Fatalf("want 7.5 MB over 30s from the kept baseline = 2, got %v", v)
	}
}

// FR-015: two readings at the same instant give no rate and no division
// error.
func TestZeroElapsed(t *testing.T) {
	r := host()
	m, _, buf := newTestModule(r, "linux")
	m.Sleep = func(context.Context, time.Duration) error { return nil } // the clock does not move
	got := collectOK(t, m)
	absent(t, got, rateKeys...)
	value(t, got, network.KeyTCPEstablished)
	if omissionRecords(buf) != 0 {
		t.Fatalf("no omission expected:\n%s", buf)
	}
}

// FR-015, FR-019: an area whose reading failed has no baseline the next time;
// its rates return one collection later, without a spike.
func TestFailedAreaHasNoBaselineNextTime(t *testing.T) {
	r := host()
	r.ifaces = [][]hostread.IfaceCounters{ifs(nic("eth0", 0, 0))}
	r.stackErr = errRead
	m, clk, _ := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)

	r.mu.Lock()
	r.stackErr = nil
	r.stacks = []hostread.StackCounters{stack(1, 1_000_000, 0, 0, 0), stack(1, 1_000_000+990, 10, 0, 0)}
	r.stackN = 0
	r.mu.Unlock()

	got := collectOK(t, m)
	absent(t, got, network.KeyTCPRetransPct, network.KeyTCPResetsPs)
	value(t, got, network.KeyTCPEstablished)
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), network.KeyTCPRetransPct); v != 1 {
		t.Fatalf("share from the first good reading: %v, want 1", v)
	}
}
