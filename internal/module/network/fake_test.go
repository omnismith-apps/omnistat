package network_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/network"
)

// fakeReader scripts the host. Interface, stack and listen-drop readings are
// consumed in order and the last one repeats, so a test that knows the first
// collection primes (two readings) indexes accordingly. It counts every call
// and is safe for concurrent use, so that -race reports contention inside the
// module, not inside the fake.
type fakeReader struct {
	mu sync.Mutex

	ifaces    [][]hostread.IfaceCounters
	ifacesErr error
	ifaceN    int

	stacks   []hostread.StackCounters
	stackErr error
	stackN   int

	listens   []uint64
	listenErr error
	listenN   int

	tw    uint64
	twErr error
	twN   int

	ct    hostread.Conntrack
	ctErr error
	ctN   int
}

func next[T any](seq []T, n int) T {
	var zero T
	if len(seq) == 0 {
		return zero
	}
	return seq[min(n, len(seq)-1)]
}

func (f *fakeReader) Interfaces(context.Context) ([]hostread.IfaceCounters, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	defer func() { f.ifaceN++ }()
	if f.ifacesErr != nil {
		return nil, f.ifacesErr
	}
	return next(f.ifaces, f.ifaceN), nil
}

func (f *fakeReader) Stack(context.Context) (hostread.StackCounters, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	defer func() { f.stackN++ }()
	if f.stackErr != nil {
		return hostread.StackCounters{}, f.stackErr
	}
	return next(f.stacks, f.stackN), nil
}

func (f *fakeReader) ListenDrops(context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	defer func() { f.listenN++ }()
	if f.listenErr != nil {
		return 0, f.listenErr
	}
	return next(f.listens, f.listenN), nil
}

func (f *fakeReader) TimeWait(context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.twN++
	return f.tw, f.twErr
}

func (f *fakeReader) Conntrack(context.Context) (hostread.Conntrack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctN++
	return f.ct, f.ctErr
}

// calls reports how often each reader method was called.
func (f *fakeReader) calls() (ifaces, stack, listen, tw, ct int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ifaceN, f.stackN, f.listenN, f.twN, f.ctN
}

var errRead = errors.New("reading unavailable")

// nic is a physical interface carrying only traffic; sw is a software one.
func nic(id string, rxB, txB uint64) hostread.IfaceCounters {
	return hostread.IfaceCounters{ID: id, Name: id, Physical: true, RxBytes: rxB, TxBytes: txB}
}

func sw(id string, rxB, txB uint64) hostread.IfaceCounters {
	return hostread.IfaceCounters{ID: id, Name: id, RxBytes: rxB, TxBytes: txB}
}

func ifs(c ...hostread.IfaceCounters) []hostread.IfaceCounters { return c }

// stack is a TCP/UDP reading.
func stack(estab, out, retrans, rsts, udpErr uint64) hostread.StackCounters {
	return hostread.StackCounters{TCPCurrEstab: estab, TCPOutSegs: out, TCPRetransSegs: retrans, TCPOutRsts: rsts, UDPInErrors: udpErr}
}

// host is a Linux host with one NIC, a quiet stack, some TIME_WAIT sockets
// and connection tracking loaded.
func host() *fakeReader {
	return &fakeReader{
		ifaces:  [][]hostread.IfaceCounters{ifs(nic("eth0", 0, 0))},
		stacks:  []hostread.StackCounters{stack(40, 1000, 0, 0, 0)},
		listens: []uint64{0},
		tw:      12,
		ct:      hostread.Conntrack{Count: 100, Max: 1000},
	}
}

// steady makes the first collection prime over two identical readings of
// each rate area (the base), and later collections read the given ones in
// order. That puts the arithmetic under test in collection 2.
func steadyIfaces(r *fakeReader, base []hostread.IfaceCounters, later ...[]hostread.IfaceCounters) {
	r.ifaces = append([][]hostread.IfaceCounters{base, base}, later...)
}

func steadyStack(r *fakeReader, base hostread.StackCounters, later ...hostread.StackCounters) {
	r.stacks = append([]hostread.StackCounters{base, base}, later...)
}

func steadyListen(r *fakeReader, base uint64, later ...uint64) {
	r.listens = append([]uint64{base, base}, later...)
}

// fakeClock is the module's injected time: Sleep advances it instantly and
// records the pause, so no test ever really waits (NFR-004).
type fakeClock struct {
	mu       sync.Mutex
	now      time.Time
	sleeps   []time.Duration
	sleepErr error
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	if c.sleepErr != nil {
		return c.sleepErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var t0 = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// newTestModule builds the module on a fake host, platform and clock, logging
// every level into the returned buffer.
func newTestModule(r *fakeReader, goos string) (*network.Module, *fakeClock, *bytes.Buffer) {
	var buf bytes.Buffer
	clk := &fakeClock{now: t0}
	return &network.Module{
		Reader: r,
		GOOS:   goos,
		Now:    clk.Now,
		Sleep:  clk.Sleep,
		Prime:  network.DefaultPrime,
		Log:    slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, clk, &buf
}

// primeThenAdvance runs the priming collection and moves the clock on by
// 30s, so that collection 2's rates are over 30 seconds.
func primeThenAdvance(t *testing.T, m *network.Module, clk *fakeClock) {
	t.Helper()
	collectOK(t, m)
	clk.Advance(30 * time.Second)
}

// collectOK collects once and returns the observations keyed by manifest key.
func collectOK(t *testing.T, m *network.Module) map[string]any {
	t.Helper()
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return byKey(t, obs)
}

func byKey(t *testing.T, obs []module.Observation) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, o := range obs {
		if _, dup := out[o.Key]; dup {
			t.Fatalf("key %q observed twice in one collection", o.Key)
		}
		out[o.Key] = o.Value
	}
	return out
}

// value returns a float observation or fails the test.
func value(t *testing.T, got map[string]any, key string) float64 {
	t.Helper()
	v, ok := got[key]
	if !ok {
		t.Fatalf("%s not observed; got %v", key, got)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("%s: want float64, got %T", key, v)
	}
	return f
}

// absent fails the test if any of keys was observed.
func absent(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := got[k]; ok {
			t.Errorf("%s must not be observed, got %v", k, v)
		}
	}
}

// omissionRecords counts the one-per-collection omission records (FR-022).
func omissionRecords(buf *bytes.Buffer) int {
	return strings.Count(buf.String(), "observations omitted")
}

var ifaceKeys = []string{
	network.KeyRxMbps, network.KeyTxMbps, network.KeyRxPps, network.KeyTxPps,
	network.KeyRxErrorsPs, network.KeyTxErrorsPs, network.KeyRxDropsPs, network.KeyTxDropsPs,
}

var rateKeys = append(append([]string{}, ifaceKeys...),
	network.KeyTCPRetransPct, network.KeyTCPResetsPs, network.KeyTCPListenDropsPs, network.KeyUDPErrorsPs)
