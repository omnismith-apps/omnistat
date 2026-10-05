package collect_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/moduletest"
)

// spec 012 FR-018 (amends 003 FR-010): a failure is a warning the first time
// it happens with a reason; repeats are counted at debug and reported through
// TakeFailureCounts; a new reason warns again; recovery says how many.
func TestScheduler_FailureStreaks(t *testing.T) {
	script := []error{errors.New("connection refused"), errors.New("connection refused"), errors.New("connection refused"),
		errors.New("i/o timeout"), nil, nil}
	c := &counter{collect: func(_ context.Context, n int32) ([]module.Observation, error) {
		if err := script[min(int(n), len(script))-1]; err != nil {
			return nil, err
		}
		return []module.Observation{{Key: "model", Value: "x"}}, nil
	}}
	m := moduletest.WithProvider(moduletest.Probe(), 10*time.Second, c.fn)
	sources, _, err := collect.Sources(desiredFor(t, m), []module.Module{m}, nil, "linux")
	if err != nil {
		t.Fatal(err)
	}
	clock := collect.NewFakeClock(t0)
	var logs bytes.Buffer
	s, _, _ := start(t, sources, clock, &logs)
	<-s.FirstRound()
	for call := int32(2); call <= 6; call++ {
		waitFor(t, "parked", func() bool { return clock.Sleepers() == 1 })
		if call == 4 {
			if got := s.TakeFailureCounts(); got["probe"] != 2 {
				t.Fatalf("repeats before the new reason = %v, want probe=2", got)
			}
		}
		clock.Advance(10 * time.Second)
		waitFor(t, "next call", func() bool { return c.calls.Load() == call })
	}
	waitFor(t, "parked", func() bool { return clock.Sleepers() == 1 })
	out := logs.String()
	if n := strings.Count(out, `level=WARN msg="collection failed" module=probe error="connection refused"`); n != 1 {
		t.Errorf("refused warned %d times, want once:\n%s", n, out)
	}
	if n := strings.Count(out, `msg="collection failed again"`); n != 2 {
		t.Errorf("repeats logged at debug %d times, want 2:\n%s", n, out)
	}
	if !strings.Contains(out, `level=WARN msg="collection failed" module=probe error="i/o timeout"`) {
		t.Errorf("a new reason must warn again:\n%s", out)
	}
	if n := strings.Count(out, `msg="collection recovered" module=probe after_failures=4`); n != 1 {
		t.Errorf("recovery logged %d times, want once with after_failures=4:\n%s", n, out)
	}
	if got := s.TakeFailureCounts(); len(got) != 0 {
		t.Fatalf("counts after take: %v", got)
	}
}
