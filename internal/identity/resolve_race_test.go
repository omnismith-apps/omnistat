package identity_test

import (
	"context"
	"sync"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/identity"
)

// US-1/3: concurrent first runs on the same machine → exactly one entity, all
// processes resolve to it.
func TestResolve_ConcurrentFirstRun(t *testing.T) {
	f := setup(t)
	const n = 8
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := identity.Resolve(context.Background(), f.api, f.target, "same", false, quiet)
			ids[i], errs[i] = h.EntityID, err
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("run %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("run %d resolved %s, run 0 resolved %s", i, ids[i], ids[0])
		}
	}
	// The fake has no server-side uniqueness, so several creates may land;
	// what matters is that every process converged on the oldest (FR-013).
	ents := f.srv.Entities()
	if len(ents) == 0 || ids[0] != ents[0].ID {
		t.Fatalf("entities %+v ids %v", ents, ids)
	}
}
