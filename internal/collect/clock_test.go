package collect_test

import (
	"context"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
)

func TestFakeClock(t *testing.T) {
	c := collect.NewFakeClock(t0)
	woke := make(chan time.Duration, 3)
	for _, d := range []time.Duration{5 * time.Second, 15 * time.Second, 10 * time.Second} {
		go func() {
			_ = c.Sleep(context.Background(), d)
			woke <- d
		}()
	}
	waitFor(t, "sleepers", func() bool { return c.Sleepers() == 3 })
	c.Advance(10 * time.Second)
	if a, b := <-woke, <-woke; a+b != 15*time.Second {
		t.Fatalf("woke: %v %v", a, b)
	}
	if c.Sleepers() != 1 || !c.Now().Equal(t0.Add(10*time.Second)) {
		t.Fatalf("state: %d %v", c.Sleepers(), c.Now())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); err == nil {
		t.Fatal("cancelled sleep must return an error")
	}
	if c.Sleepers() != 1 {
		t.Fatalf("cancelled sleeper must be removed: %d", c.Sleepers())
	}
	if err := c.Sleep(context.Background(), 0); err != nil {
		t.Fatal("zero sleep returns at once")
	}
	if err := (collect.RealClock{}).Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// Deadlines expire on Advance and never count as sleepers.
	dctx, dcancel := c.WithDeadline(context.Background(), 5*time.Second)
	defer dcancel()
	if c.Sleepers() != 1 || dctx.Err() != nil {
		t.Fatalf("deadline must not count: %d %v", c.Sleepers(), dctx.Err())
	}
	c.Advance(5 * time.Second)
	waitFor(t, "deadline expiry", func() bool { return dctx.Err() != nil })
	rctx, rcancel := (collect.RealClock{}).WithDeadline(context.Background(), time.Hour)
	rcancel()
	if rctx.Err() == nil {
		t.Fatal("real deadline cancel")
	}
}
