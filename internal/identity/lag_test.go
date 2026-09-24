package identity_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/settle"
)

// noSleep is a settle policy that waits for nothing but records each wait.
func noSleep(waits *[]time.Duration) settle.Policy {
	return settle.Policy{
		Delays: settle.Default().Delays,
		Sleep: func(ctx context.Context, d time.Duration) error {
			*waits = append(*waits, d)
			return ctx.Err()
		},
	}
}

// FR-012 on an asynchronous platform: the re-search after the create waits
// until the new entity is searchable, so that the next resolution — the next
// process, or the reuse step of a test — finds it instead of creating another.
func TestResolve_WaitsForOwnCreateToBeSearchable(t *testing.T) {
	f := setup(t)
	f.srv.SearchLag = 2 // invisible to the next two searches
	var waits []time.Duration
	ctx := context.Background()

	h, err := identity.ResolveWith(ctx, f.api, f.target, "lag-1", false, quiet, noSleep(&waits))
	if err != nil || !h.Created || len(h.Duplicates) != 0 {
		t.Fatalf("create: %+v %v", h, err)
	}
	if len(waits) != 2 {
		t.Fatalf("waited %v; want two waits before the entity became searchable", waits)
	}
	// Once Resolve has returned, the entity is searchable: reuse, no create.
	h2, err := identity.ResolveWith(ctx, f.api, f.target, "lag-1", false, quiet, noSleep(&waits))
	if err != nil || h2.Created || h2.EntityID != h.EntityID {
		t.Fatalf("reuse: %+v %v", h2, err)
	}
	if n := len(f.srv.Entities()); n != 1 {
		t.Fatalf("%d entities; lag must not produce a duplicate", n)
	}
}

// FR-012: a search that never catches up within the budget is not an error.
// The run keeps the entity it created and says the concurrency check was
// inconclusive; a duplicate, if one exists, is resolved by FR-013 next time.
func TestResolve_LagBeyondBudget(t *testing.T) {
	f := setup(t)
	f.srv.SearchLag = 100
	var waits []time.Duration
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	h, err := identity.ResolveWith(context.Background(), f.api, f.target, "slow", false, log, noSleep(&waits))
	if err != nil || !h.Created || h.EntityID != f.srv.Entities()[0].ID {
		t.Fatalf("host: %+v %v", h, err)
	}
	if len(waits) != len(settle.Default().Delays) {
		t.Fatalf("waited %v; the whole budget must be spent, and no more", waits)
	}
	if !strings.Contains(buf.String(), "not yet searchable") {
		t.Fatalf("expected a warning about the unsettled search:\n%s", buf.String())
	}
}

// FR-012/FR-013 under lag: another process created the same identity just
// before us. Our re-search waits for our own entity, sees both, and picks the
// older one — not whichever happened to become visible first.
func TestResolve_ConcurrentCreateUnderLag(t *testing.T) {
	f := setup(t)
	f.srv.SearchLag = 1
	var once sync.Once
	f.srv.Before = func(r *http.Request) {
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/entities/template/") {
			once.Do(func() { f.srv.AddEntityLocked("host", map[string]any{"machine_id": "twin"}, "2026-09-19T00:00:00Z") })
		}
	}
	var waits []time.Duration
	h, err := identity.ResolveWith(context.Background(), f.api, f.target, "twin", false, quiet, noSleep(&waits))
	if err != nil {
		t.Fatal(err)
	}
	ents := f.srv.Entities()
	if len(ents) != 2 || h.EntityID != ents[0].ID || len(h.Duplicates) != 2 {
		t.Fatalf("host %+v entities %+v", h, ents)
	}
}
