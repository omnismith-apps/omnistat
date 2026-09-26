package disk_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
)

// FR-014: the retained reading is the provider's own mutable state. The core
// never overlaps a module's collections (003 FR-011), but the state must not be
// the kind of thing that corrupts if something ever does.
func TestCollect_ConcurrentCallsAreSafe(t *testing.T) {
	r := steady(devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0)))
	r.usage = vol(100*gib, 40*gib, 60*gib, 0, 0) // exercises the once-only notice too
	m, clk, _ := newTestModule(r, "linux")

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				clk.Advance(time.Second)
				if _, err := m.Collect(context.Background()); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}
