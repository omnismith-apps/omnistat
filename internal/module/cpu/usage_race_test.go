package cpu_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/module/cpu"
)

// lockedReader is a fakeReader that is itself safe to call concurrently, so
// that -race reports contention inside the module rather than inside the fake.
type lockedReader struct {
	mu sync.Mutex
	r  *fakeReader
}

func (l *lockedReader) Times(ctx context.Context) (cpu.Times, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Times(ctx)
}

func (l *lockedReader) LoadAvg(ctx context.Context) (cpu.Load, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.LoadAvg(ctx)
}

func (l *lockedReader) Counts(ctx context.Context) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Counts(ctx)
}

func (l *lockedReader) Model(ctx context.Context) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Model(ctx)
}

// FR-014: the retained reading is the provider's own mutable state. The core
// never overlaps a module's collections (003 FR-011), but the state must not be
// the kind of thing that corrupts if something ever does.
func TestCollect_ConcurrentCallsAreSafe(t *testing.T) {
	r := fake()
	r.times = []cpu.Times{{User: 100, Idle: 900}}
	m := newTestModule(t, &lockedReader{r: r})
	m.Sleep = func(context.Context, time.Duration) error { return nil }

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := m.Collect(context.Background()); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
