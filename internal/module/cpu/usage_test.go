package cpu_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/module/cpu"
)

// FR-005/FR-013: usage is the non-idle share of the CPU time that elapsed
// between two readings, aggregate across all CPUs, clamped to [0, 100]; a pair
// that cannot yield a percentage yields none rather than a wrong one.
func TestPercent(t *testing.T) {
	cases := []struct {
		name       string
		prev, cur  cpu.Times
		want       float64
		wantSample bool
	}{
		{
			name: "fully idle",
			prev: cpu.Times{Idle: 1000}, cur: cpu.Times{Idle: 2000},
			want: 0, wantSample: true,
		},
		{
			name: "fully busy",
			prev: cpu.Times{User: 1000}, cur: cpu.Times{User: 2000},
			want: 100, wantSample: true,
		},
		{
			name: "half busy",
			prev: cpu.Times{User: 100, Idle: 100}, cur: cpu.Times{User: 200, Idle: 200},
			want: 50, wantSample: true,
		},
		{
			// FR-005: an 8-core host with every core saturated reports 100,
			// not 800 — the counters are already aggregate.
			name: "eight saturated cores stay at 100",
			prev: cpu.Times{User: 1000}, cur: cpu.Times{User: 1000 + 8*100},
			want: 100, wantSample: true,
		},
		{
			// iowait counts as idle, not as busy.
			name: "iowait is idle",
			prev: cpu.Times{}, cur: cpu.Times{Iowait: 100, User: 100},
			want: 50, wantSample: true,
		},
		{
			// Every non-idle state counts as busy.
			name: "steal and irq are busy",
			prev: cpu.Times{},
			cur:  cpu.Times{Steal: 25, Irq: 10, Softirq: 5, System: 10, Nice: 10, Idle: 40},
			want: 60, wantSample: true,
		},
		{
			// FR-013: two readings inside one tick.
			name: "no elapsed time",
			prev: cpu.Times{User: 100, Idle: 900}, cur: cpu.Times{User: 100, Idle: 900},
			wantSample: false,
		},
		{
			// FR-013: a counter that went backwards (suspend/resume, hotplug,
			// container counter reset).
			name: "counter went backwards",
			prev: cpu.Times{User: 500, Idle: 5000}, cur: cpu.Times{User: 100, Idle: 900},
			wantSample: false,
		},
		{
			// A single field regressing while the total still advances is
			// just as untrustworthy.
			name: "one field regressed",
			prev: cpu.Times{User: 500, Idle: 100}, cur: cpu.Times{User: 400, Idle: 5000},
			wantSample: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := cpu.Percent(c.prev, c.cur)
			if ok != c.wantSample {
				t.Fatalf("ok = %v, want %v (got %v)", ok, c.wantSample, got)
			}
			if !ok {
				return
			}
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("value must be finite, got %v", got)
			}
			if got < 0 || got > 100 {
				t.Fatalf("value must be clamped to [0,100], got %v", got)
			}
			if math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// Guest time is already counted inside User (and guest-nice inside Nice) by the
// Linux kernel. Times has no field for it, so a host running a busy guest must
// still report 100 rather than half of it (plan §3).
func TestPercent_GuestIsNotDoubleCounted(t *testing.T) {
	// 100 ticks of user time, all of which were guest time.
	prev := cpu.Times{User: 0, Idle: 0}
	cur := cpu.Times{User: 100, Idle: 0}
	got, ok := cpu.Percent(prev, cur)
	if !ok || math.Abs(got-100) > 1e-9 {
		t.Fatalf("got %v (ok=%v), want 100 — guest time must not inflate the denominator", got, ok)
	}
}

// Total and IdleTime are the two sums the formula depends on; pin them so a
// later field addition cannot silently change the denominator.
func TestTimesSums(t *testing.T) {
	x := cpu.Times{User: 1, Nice: 2, System: 4, Idle: 8, Iowait: 16, Irq: 32, Softirq: 64, Steal: 128}
	if got := x.Total(); got != 255 {
		t.Fatalf("Total = %v, want 255", got)
	}
	if got := cpu.IdleTime(x); got != 24 {
		t.Fatalf("IdleTime = %v, want 24 (idle + iowait)", got)
	}
}

// sleepRecorder counts prime pauses and can fail them, without real time.
type sleepRecorder struct {
	calls int
	durs  []time.Duration
	err   error
}

func (s *sleepRecorder) fn(ctx context.Context, d time.Duration) error {
	s.calls++
	s.durs = append(s.durs, d)
	if s.err != nil {
		return s.err
	}
	return nil
}

// FR-012: with no previous reading the provider primes itself inside the call —
// a second reading after a bounded pause — so a one-shot `omnistat run`
// publishes a real measurement instead of nothing (US-2/1).
func TestCollect_FirstCallPrimes(t *testing.T) {
	r := fake()
	r.times = []cpu.Times{{User: 100, Idle: 900}, {User: 200, Idle: 1800}}
	m := newTestModule(t, r)
	sr := &sleepRecorder{}
	m.Sleep = sr.fn

	obs := collectOK(t, m)
	if sr.calls != 1 || sr.durs[0] != cpu.DefaultPrime {
		t.Fatalf("prime: calls=%d durs=%v", sr.calls, sr.durs)
	}
	// 100 busy of 1000 elapsed.
	if got, ok := obs["usage"].(float64); !ok || math.Abs(got-10) > 1e-9 {
		t.Fatalf("usage: %v", obs["usage"])
	}
	if r.timesN != 2 {
		t.Fatalf("first collection must take two readings, took %d", r.timesN)
	}
}

// FR-011: every later collection measures across the whole interval since the
// previous reading and does not pause at all.
func TestCollect_LaterCallsUseTheStoredReading(t *testing.T) {
	r := fake()
	r.times = []cpu.Times{
		{User: 100, Idle: 900},  // first call, reading 1
		{User: 200, Idle: 1800}, // first call, reading 2 (baseline)
		{User: 700, Idle: 2300}, // second call: 500 busy of 1000
	}
	m := newTestModule(t, r)
	sr := &sleepRecorder{}
	m.Sleep = sr.fn

	collectOK(t, m)
	before := sr.calls
	obs := collectOK(t, m)
	if sr.calls != before {
		t.Fatalf("a later collection must not pause: %d pauses", sr.calls-before)
	}
	if got, ok := obs["usage"].(float64); !ok || math.Abs(got-50) > 1e-9 {
		t.Fatalf("usage: %v", obs["usage"])
	}
	if r.timesN != 3 {
		t.Fatalf("a later collection must take one reading, total %d", r.timesN)
	}
}

// FR-012/US-2/2: when the deadline is too short to prime, usage is skipped and
// everything else is still collected — a tight deadline costs one value, not
// the collection.
func TestCollect_TightDeadlineSkipsUsageButKeepsDimensions(t *testing.T) {
	r := fake()
	m := newTestModule(t, r)
	sr := &sleepRecorder{}
	m.Sleep = sr.fn

	ctx, cancel := context.WithDeadline(context.Background(), m.Now().Add(10*time.Millisecond))
	defer cancel()
	obs, err := m.Collect(ctx)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := byKey(obs)
	if _, present := got["usage"]; present {
		t.Fatal("usage must be absent when there is no time to measure it")
	}
	if sr.calls != 0 {
		t.Fatalf("must not pause when the deadline forbids it: %d", sr.calls)
	}
	if got["model"] == nil || got["cores"] == nil || got["arch"] == nil {
		t.Fatalf("dimensions must survive: %v", got)
	}
	// The reading still becomes the baseline, so the next call measures.
	obs2, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, present := byKey(obs2)["usage"]; !present {
		t.Fatal("the skipped prime must still leave a baseline behind")
	}
}

// FR-014: a cancellation during the prime pause must leave the retained
// reading consistent — the new one becomes the baseline, never a mixture.
func TestCollect_CancelledPrimeLeavesAConsistentBaseline(t *testing.T) {
	r := fake()
	// The cancelled call takes exactly one reading, so the next call reads the
	// second entry: 500 busy of 1000 elapsed against the retained baseline.
	r.times = []cpu.Times{{User: 100, Idle: 900}, {User: 600, Idle: 1400}}
	m := newTestModule(t, r)
	m.Sleep = (&sleepRecorder{err: context.Canceled}).fn

	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatalf("a cancelled prime is not a failed collection: %v", err)
	}
	if _, present := byKey(obs)["usage"]; present {
		t.Fatal("no usage may be reported from a cancelled prime")
	}
	if r.timesN != 1 {
		t.Fatalf("the second reading must not be taken after cancellation: %d", r.timesN)
	}
	// Baseline is the first reading (100/900); next call reads 600/1400,
	// i.e. 500 busy of 1000.
	m.Sleep = (&sleepRecorder{}).fn
	next := collectOK(t, m)
	if got, ok := next["usage"].(float64); !ok || math.Abs(got-50) > 1e-9 {
		t.Fatalf("usage after a cancelled prime: %v", next["usage"])
	}
}

// FR-014: a failed reading must not disturb the baseline that is already held.
func TestCollect_ReadErrorKeepsTheBaseline(t *testing.T) {
	r := fake()
	r.times = []cpu.Times{{User: 100, Idle: 900}, {User: 200, Idle: 1800}, {User: 700, Idle: 2300}}
	m := newTestModule(t, r)
	m.Sleep = (&sleepRecorder{}).fn
	collectOK(t, m) // baseline = 200/1800

	r.timesErr = errRead
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatalf("one failed reading must not fail the collection: %v", err)
	}
	if _, present := byKey(obs)["usage"]; present {
		t.Fatal("usage must be absent when the reading failed")
	}

	r.timesErr = nil
	next := collectOK(t, m) // reads 700/2300 against the intact baseline
	if got, ok := next["usage"].(float64); !ok || math.Abs(got-50) > 1e-9 {
		t.Fatalf("baseline was disturbed: %v", next["usage"])
	}
}

// FR-005: usage is published rounded to two decimal places — the precision of
// the load averages the OS reports next to it — not as the full float64 of
// the division (2.6550327204792796 in the first sandbox run).
func TestCollect_UsageRoundedToTwoDecimals(t *testing.T) {
	r := fake()
	r.times = []cpu.Times{{User: 0, Idle: 0}, {User: 2, Idle: 1}} // 2 busy of 3: 66.666…
	m := newTestModule(t, r)
	obs := collectOK(t, m)
	if got := obs["usage"]; got != 66.67 {
		t.Fatalf("usage = %v, want 66.67", got)
	}
}
