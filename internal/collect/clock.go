package collect

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Clock is the scheduler's view of time, injectable so that scheduling is
// deterministic in tests (spec 003 NFR-006).
type Clock interface {
	Now() time.Time
	// Sleep blocks for d or until ctx is done, returning ctx.Err() in the
	// latter case.
	Sleep(ctx context.Context, d time.Duration) error
	// WithDeadline derives a context that is cancelled after d.
	WithDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc)
}

// RealClock is the wall clock.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// Sleep implements Clock.
func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
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

// WithDeadline implements Clock.
func (RealClock) WithDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

// FakeClock is a manual clock for tests: time moves only through Advance,
// which wakes every Sleep and expires every WithDeadline whose time has
// come, earliest first.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
}

type waiter struct {
	at time.Time
	ch chan struct{}
	// deadline waiters are not reported by Sleepers.
	deadline bool
}

// NewFakeClock returns a clock stopped at start.
func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

// Now implements Clock.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep implements Clock.
func (c *FakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	w := &waiter{ch: make(chan struct{})}
	c.mu.Lock()
	w.at = c.now.Add(d)
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()
	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		c.remove(w)
		return ctx.Err()
	}
}

func (c *FakeClock) remove(w *waiter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, x := range c.waiters {
		if x == w {
			c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
			return
		}
	}
}

// Advance moves the clock forward by d, waking sleepers whose deadline has
// passed, earliest first.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	sort.SliceStable(c.waiters, func(i, j int) bool { return c.waiters[i].at.Before(c.waiters[j].at) })
	var due, rest []*waiter
	for _, w := range c.waiters {
		if !w.at.After(target) {
			due = append(due, w)
		} else {
			rest = append(rest, w)
		}
	}
	c.waiters = rest
	c.now = target
	c.mu.Unlock()
	for _, w := range due {
		close(w.ch)
	}
}

// WithDeadline implements Clock: the context expires when Advance reaches
// its deadline.
func (c *FakeClock) WithDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	w := &waiter{ch: make(chan struct{}), deadline: true}
	c.mu.Lock()
	w.at = c.now.Add(d)
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()
	go func() {
		select {
		case <-w.ch:
			cancel()
		case <-ctx.Done():
			c.remove(w)
		}
	}()
	return ctx, cancel
}

// Sleepers reports how many Sleep calls are pending (deadlines excluded) —
// tests use it to know that goroutines have parked before advancing.
func (c *FakeClock) Sleepers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, w := range c.waiters {
		if !w.deadline {
			n++
		}
	}
	return n
}
