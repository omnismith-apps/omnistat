package identity_test

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

var quiet = slog.New(slog.DiscardHandler)

type fixture struct {
	srv    *omnitest.Server
	api    *omni.Client
	target identity.Target
}

func setup(t *testing.T) fixture {
	t.Helper()
	srv := omnitest.New()
	t.Cleanup(srv.Close)
	srv.AddAttribute("machine_id", "string")
	srv.AddAttribute("hostname", "string")
	tid := srv.AddTemplate("host", "Host", "machine_id", "hostname")
	c, err := omni.New(omni.Settings{BaseURL: srv.URL, Token: omnitest.Token, ProjectID: omnitest.ProjectID, Retries: 0})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{srv, c, identity.Target{TemplateSlug: "host", TemplateID: tid, AttributeSlug: "machine_id"}}
}

// requests counts the recorded requests by method and path prefix.
func requests(srv *omnitest.Server) map[string]int {
	out := map[string]int{}
	for _, r := range srv.Requests() {
		switch {
		case strings.HasSuffix(r.Path, "/by-key"):
			out[r.Method+" by-key"]++
		case strings.HasPrefix(r.Path, "/entities/search/"):
			out["search"]++
		case r.Method == "PATCH":
			out["patch"]++
		default:
			out[r.Method+" "+r.Path]++
		}
	}
	return out
}

// spec 011 US-3/3, FR-011, NFR-001: a fresh project gets one host entity
// holding its key, with machine_id set (002 FR-014); the next start finds it
// with one lookup and writes nothing.
func TestResolveHost_CreateThenFind(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	h, err := identity.ResolveHost(ctx, f.api, f.target, "id-1", false, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if h.Outcome != identity.Created || !h.Created || h.EntityID == "" {
		t.Fatalf("host: %+v", h)
	}
	ents := f.srv.Entities()
	if len(ents) != 1 || ents[0].ExternalKey != "id-1" || ents[0].Values["machine_id"] != "id-1" || len(ents[0].Values) != 1 {
		t.Fatalf("entities: %+v", ents)
	}
	if got := requests(f.srv); got["GET by-key"] != 1 || got["search"] != 1 || got["PUT by-key"] != 1 {
		t.Fatalf("fresh start requests: %v", got)
	}

	f.srv.ResetRequests()
	h2, err := identity.ResolveHost(ctx, f.api, f.target, "id-1", false, quiet)
	if err != nil || h2.Outcome != identity.Found || h2.EntityID != h.EntityID {
		t.Fatalf("find: %+v %v", h2, err)
	}
	if got := requests(f.srv); len(got) != 1 || got["GET by-key"] != 1 {
		t.Fatalf("a start that finds its host must make one lookup and no write: %v", got)
	}
}

// spec 011 US-3/1, FR-012: a host entity from an earlier version (no key) is
// adopted: it gets the key and no entity is created.
func TestResolveHost_AdoptsLegacy(t *testing.T) {
	f := setup(t)
	legacy := f.srv.AddEntity("host", map[string]any{"machine_id": "id-1"}, "")
	h, err := identity.ResolveHost(context.Background(), f.api, f.target, "id-1", false, quiet)
	if err != nil || h.Outcome != identity.Adopted || h.EntityID != legacy {
		t.Fatalf("adopt: %+v %v", h, err)
	}
	ents := f.srv.Entities()
	if len(ents) != 1 || ents[0].ExternalKey != "id-1" {
		t.Fatalf("entities: %+v", ents)
	}
	h2, err := identity.ResolveHost(context.Background(), f.api, f.target, "id-1", false, quiet)
	if err != nil || h2.Outcome != identity.Found || h2.EntityID != legacy {
		t.Fatalf("after adoption: %+v %v", h2, err)
	}
}

// spec 011 US-3/2: legacy duplicates → the oldest is adopted, the others are
// listed and untouched.
func TestResolveHost_AdoptsOldestDuplicate(t *testing.T) {
	f := setup(t)
	newer := f.srv.AddEntity("host", map[string]any{"machine_id": "id-1"}, "2026-01-02T00:00:00Z")
	older := f.srv.AddEntity("host", map[string]any{"machine_id": "id-1"}, "2026-01-01T00:00:00Z")
	h, err := identity.ResolveHost(context.Background(), f.api, f.target, "id-1", false, quiet)
	if err != nil || h.Outcome != identity.Adopted || h.EntityID != older {
		t.Fatalf("adopt: %+v %v", h, err)
	}
	if len(h.Duplicates) != 2 || h.Duplicates[0] != older || h.Duplicates[1] != newer {
		t.Fatalf("duplicates: %v", h.Duplicates)
	}
	for _, e := range f.srv.Entities() {
		if e.ID == newer && e.ExternalKey != "" {
			t.Fatalf("the newer duplicate was keyed: %+v", e)
		}
	}
}

// spec 011 US-4/4, FR-012, NFR-003: an adopted entity that already holds
// another key keeps it; omnistat uses the entity and warns.
func TestResolveHost_NeverOverwritesAKey(t *testing.T) {
	f := setup(t)
	id := f.srv.AddEntity("host", map[string]any{"machine_id": "id-1"}, "")
	f.srv.SetEntityKey(id, "proxmox:vm-101")
	h, err := identity.ResolveHost(context.Background(), f.api, f.target, "id-1", false, quiet)
	if err != nil || h.Outcome != identity.AdoptedOtherKey || h.EntityID != id || h.OtherKey != "proxmox:vm-101" {
		t.Fatalf("adopt: %+v %v", h, err)
	}
	if e := f.srv.Entities()[0]; e.ExternalKey != "proxmox:vm-101" {
		t.Fatalf("key overwritten: %+v", e)
	}
	if got := requests(f.srv); got["patch"] != 0 || got["PUT by-key"] != 0 {
		t.Fatalf("writes made: %v", got)
	}
}

// spec 011 FR-012: another record took the key between the search and the
// adoption; that record is used.
func TestResolveHost_AdoptionLosesTheKey(t *testing.T) {
	f := setup(t)
	f.srv.AddEntity("host", map[string]any{"machine_id": "id-1"}, "2026-01-01T00:00:00Z")
	winner := f.srv.AddEntity("host", map[string]any{"machine_id": "other"}, "2026-01-02T00:00:00Z")
	f.srv.Before = func(r *http.Request) {
		if r.Method == "PATCH" {
			f.srv.SetEntityKeyLocked(winner, "id-1")
			f.srv.Before = nil
		}
	}
	h, err := identity.ResolveHost(context.Background(), f.api, f.target, "id-1", false, quiet)
	if err != nil || h.Outcome != identity.Found || h.EntityID != winner {
		t.Fatalf("resolve: %+v %v", h, err)
	}
}

// spec 011 FR-020, 002 FR-017: dry-run reports would-adopt and would-create
// and writes nothing.
func TestResolveHost_DryRun(t *testing.T) {
	f := setup(t)
	h, err := identity.ResolveHost(context.Background(), f.api, f.target, "id-1", true, quiet)
	if err != nil || h.Outcome != identity.WouldCreate || !h.WouldCreate || h.EntityID != "" {
		t.Fatalf("would create: %+v %v", h, err)
	}
	legacy := f.srv.AddEntity("host", map[string]any{"machine_id": "id-1"}, "")
	h, err = identity.ResolveHost(context.Background(), f.api, f.target, "id-1", true, quiet)
	if err != nil || h.Outcome != identity.WouldAdopt || h.EntityID != legacy {
		t.Fatalf("would adopt: %+v %v", h, err)
	}
	if got := requests(f.srv); got["patch"] != 0 || got["PUT by-key"] != 0 {
		t.Fatalf("dry-run wrote: %v", got)
	}
	if e := f.srv.Entities()[0]; e.ExternalKey != "" {
		t.Fatalf("dry-run set a key: %+v", e)
	}
}

// spec 011 US-1/3, FR-011: concurrent first starts end with exactly one
// keyed host, whatever the interleaving.
func TestResolveHost_ConcurrentFirstStarts(t *testing.T) {
	f := setup(t)
	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := identity.ResolveHost(context.Background(), f.api, f.target, "id-1", false, quiet)
			ids[i], errs[i] = h.EntityID, err
		}(i)
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("start %d: %s %v (first: %s)", i, ids[i], errs[i], ids[0])
		}
	}
	if len(f.srv.Entities()) != 1 {
		t.Fatalf("entities: %+v", f.srv.Entities())
	}
}

// spec 011 FR-011: an upsert answered with a conflict is retried.
func TestResolveHost_UpsertConflictRetried(t *testing.T) {
	f := setup(t)
	f.srv.UpsertRace = 2
	h, err := identity.ResolveHost(context.Background(), f.api, f.target, "id-1", false, quiet)
	if err != nil || h.Outcome != identity.Created {
		t.Fatalf("resolve: %+v %v", h, err)
	}
}

// spec 011 FR-008: keys are validated before any request.
func TestValidKey(t *testing.T) {
	for _, bad := range []string{"", " a", "a ", strings.Repeat("x", 129), "a\nb", "a\x00b"} {
		if identity.ValidKey(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, good := range []string{"a", "rack-ups-1", "stripe:cus_1/x y", strings.Repeat("é", 128)} {
		if err := identity.ValidKey(good); err != nil {
			t.Errorf("%q rejected: %v", good, err)
		}
	}
	f := setup(t)
	if _, err := identity.ResolveHost(context.Background(), f.api, f.target, " id", false, quiet); err == nil {
		t.Fatal("invalid key resolved")
	}
	if len(f.srv.Requests()) != 0 {
		t.Fatalf("requests made for an invalid key: %v", f.srv.Requests())
	}
}

// Unresolved schema fails before any request (002 FR-016).
func TestResolveHost_NeedsTarget(t *testing.T) {
	f := setup(t)
	if _, err := identity.ResolveHost(context.Background(), f.api, identity.Target{}, "id-1", false, quiet); err == nil {
		t.Fatal("resolved without a target")
	}
}
