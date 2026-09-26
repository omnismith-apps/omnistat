package network_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
)

// FR-015: the retained reading is the provider's own mutable state. The core
// never overlaps a module's collections (003 FR-011), but the state must not be
// the kind of thing that corrupts if something ever does.
func TestCollect_ConcurrentCallsAreSafe(t *testing.T) {
	r := host()
	r.ctErr = hostread.ErrNoConntrack                             // exercises the once-only notice
	r.ifaces = [][]hostread.IfaceCounters{ifs(sw("veth0", 0, 0))} // and the other one
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
