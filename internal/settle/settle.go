// Package settle waits, briefly and boundedly, for omnistat's own writes to
// become visible to the platform's reads.
//
// Omnismith processes writes asynchronously: a create or update is accepted
// and answered before search and discovery reflect it (measured at roughly
// 100–300 ms on the local API, spec 005 implementation notes). A caller that
// reads back what it has just written — identity's re-search after creating
// the host entity (spec 002 FR-012), schema reconciliation's re-read after a
// create was refused as already existing (spec 001 FR-024) — must not treat
// "not there yet" as "not there". It re-reads with this package until its own
// condition holds or a fixed budget is spent, and then proceeds on what it
// knows from its own write responses.
//
// The policy is injectable so that tests never sleep (constitution V).
package settle

import (
	"context"
	"time"
)

// Policy is how long a reader waits for its write to become visible: it reads
// once immediately, then once after each delay in turn.
type Policy struct {
	Delays []time.Duration
	Sleep  func(ctx context.Context, d time.Duration) error
}

// Default waits up to about 3s in total, backing off from 100ms — an order of
// magnitude above the lag measured on the local API, and short enough that a
// one-shot run does not stall when a write never appears.
func Default() Policy {
	return Policy{
		Delays: []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond},
		Sleep:  Sleep,
	}
}

// None reads exactly once: for callers and tests that must not wait.
func None() Policy { return Policy{} }

// Sleep waits for d or until ctx is done, whichever comes first.
func Sleep(ctx context.Context, d time.Duration) error {
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

// Until calls read until done accepts its result, or the policy's delays are
// spent, or ctx ends. It returns the last successful result and whether done
// accepted it. A read error is returned at once: lag shows up as a stale
// answer, never as an error, so an error is not something to wait out. A
// cancelled wait is not an error either — the caller gets the last result and
// settled == false, exactly as if the budget had run out.
func Until[T any](ctx context.Context, p Policy, read func(context.Context) (T, error), done func(T) bool) (last T, settled bool, err error) {
	for i := 0; ; i++ {
		last, err = read(ctx)
		if err != nil {
			return last, false, err
		}
		if done(last) {
			return last, true, nil
		}
		if i >= len(p.Delays) {
			return last, false, nil
		}
		sleep := p.Sleep
		if sleep == nil {
			sleep = Sleep
		}
		if sleep(ctx, p.Delays[i]) != nil {
			return last, false, nil
		}
	}
}
