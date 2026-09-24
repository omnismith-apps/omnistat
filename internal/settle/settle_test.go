package settle_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/settle"
)

// recorder is a Policy.Sleep that never sleeps and records what was asked.
type recorder struct {
	slept []time.Duration
	fail  bool
}

func (r *recorder) sleep(ctx context.Context, d time.Duration) error {
	r.slept = append(r.slept, d)
	if r.fail {
		return context.Canceled
	}
	return ctx.Err()
}

func policy(r *recorder) settle.Policy {
	return settle.Policy{Delays: []time.Duration{1, 2, 3}, Sleep: r.sleep}
}

// A write that becomes visible on the third read settles after two waits.
func TestUntil_SettlesWhenVisible(t *testing.T) {
	r := &recorder{}
	n := 0
	got, ok, err := settle.Until(context.Background(), policy(r),
		func(context.Context) (int, error) { n++; return n, nil },
		func(v int) bool { return v == 3 })
	if err != nil || !ok || got != 3 {
		t.Fatalf("got %d ok=%v err=%v", got, ok, err)
	}
	if len(r.slept) != 2 || r.slept[0] != 1 || r.slept[1] != 2 {
		t.Fatalf("slept %v, want [1 2]", r.slept)
	}
}

// Already visible: one read, no wait.
func TestUntil_NoWaitWhenAlreadyVisible(t *testing.T) {
	r := &recorder{}
	_, ok, _ := settle.Until(context.Background(), policy(r),
		func(context.Context) (string, error) { return "x", nil },
		func(string) bool { return true })
	if !ok || len(r.slept) != 0 {
		t.Fatalf("ok=%v slept=%v", ok, r.slept)
	}
}

// Never visible: every delay is spent once, and the last result is returned
// unsettled rather than as an error.
func TestUntil_BudgetSpent(t *testing.T) {
	r := &recorder{}
	reads := 0
	got, ok, err := settle.Until(context.Background(), policy(r),
		func(context.Context) (int, error) { reads++; return reads, nil },
		func(int) bool { return false })
	if err != nil || ok || got != 4 || reads != 4 || len(r.slept) != 3 {
		t.Fatalf("got=%d ok=%v err=%v reads=%d slept=%v", got, ok, err, reads, r.slept)
	}
}

// A read error is not lag: it is returned at once, without waiting.
func TestUntil_ReadErrorStops(t *testing.T) {
	r := &recorder{}
	boom := errors.New("boom")
	_, ok, err := settle.Until(context.Background(), policy(r),
		func(context.Context) (int, error) { return 0, boom },
		func(int) bool { return true })
	if !errors.Is(err, boom) || ok || len(r.slept) != 0 {
		t.Fatalf("ok=%v err=%v slept=%v", ok, err, r.slept)
	}
}

// A cancelled wait ends the loop unsettled, without an error.
func TestUntil_CancelledWait(t *testing.T) {
	r := &recorder{fail: true}
	reads := 0
	_, ok, err := settle.Until(context.Background(), policy(r),
		func(context.Context) (int, error) { reads++; return reads, nil },
		func(int) bool { return false })
	if err != nil || ok || reads != 1 {
		t.Fatalf("ok=%v err=%v reads=%d", ok, err, reads)
	}
}

// None reads once and never sleeps; Default is bounded to a few seconds.
func TestPolicies(t *testing.T) {
	reads := 0
	_, ok, _ := settle.Until(context.Background(), settle.None(),
		func(context.Context) (int, error) { reads++; return reads, nil },
		func(int) bool { return false })
	if ok || reads != 1 {
		t.Fatalf("None: ok=%v reads=%d", ok, reads)
	}
	var total time.Duration
	for _, d := range settle.Default().Delays {
		total += d
	}
	if total < time.Second || total > 5*time.Second {
		t.Fatalf("Default waits %v in total; want a bounded few seconds", total)
	}
}

// Sleep ends early on a cancelled context and returns at once for d <= 0,
// so a stopping daemon is never held by a settle wait.
func TestSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := settle.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if err := settle.Sleep(context.Background(), 0); err != nil {
		t.Fatalf("zero: %v", err)
	}
	if err := settle.Sleep(context.Background(), time.Nanosecond); err != nil {
		t.Fatalf("elapsed: %v", err)
	}
}
