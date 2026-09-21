package identity_test

import (
	"context"
	"errors"
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

// US-1/1-2, FR-010…012, FR-014: none → create (identity only), then reuse.
func TestResolve_CreateThenReuse(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	h, err := identity.Resolve(ctx, f.api, f.target, "id-1", false, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Created || h.EntityID == "" || h.Identity != "id-1" || len(h.Duplicates) != 0 {
		t.Fatalf("host: %+v", h)
	}
	ents := f.srv.Entities()
	if len(ents) != 1 || ents[0].TemplateSlug != "host" || ents[0].Values["machine_id"] != "id-1" || len(ents[0].Values) != 1 {
		t.Fatalf("entities: %+v", ents)
	}

	h2, err := identity.Resolve(ctx, f.api, f.target, "id-1", false, quiet)
	if err != nil || h2.Created || h2.EntityID != h.EntityID {
		t.Fatalf("reuse: %+v %v", h2, err)
	}
	if len(f.srv.Entities()) != 1 {
		t.Fatal("second run must not create")
	}
	// exact match: a different identity is a different host
	h3, err := identity.Resolve(ctx, f.api, f.target, "id-10", false, quiet)
	if err != nil || !h3.Created || h3.EntityID == h.EntityID {
		t.Fatalf("other identity: %+v %v", h3, err)
	}
}

// FR-013 / US-3/2: duplicates → oldest wins, all reported, nothing created.
func TestResolve_Duplicates(t *testing.T) {
	f := setup(t)
	newer := f.srv.AddEntity("host", map[string]any{"machine_id": "dup"}, "2026-09-20T10:00:00Z")
	older := f.srv.AddEntity("host", map[string]any{"machine_id": "dup"}, "2026-09-19T10:00:00Z")
	_ = f.srv.AddEntity("host", map[string]any{"machine_id": "unrelated"}, "2026-09-18T10:00:00Z")

	h, err := identity.Resolve(context.Background(), f.api, f.target, "dup", false, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if h.Created || h.EntityID != older || strings.Join(h.Duplicates, ",") != older+","+newer {
		t.Fatalf("host: %+v (older=%s newer=%s)", h, older, newer)
	}
	if len(f.srv.Entities()) != 3 {
		t.Fatal("duplicates must not trigger a create")
	}
}

// US-5, FR-017: dry-run resolves without writing.
func TestResolve_DryRun(t *testing.T) {
	f := setup(t)
	h, err := identity.Resolve(context.Background(), f.api, f.target, "id-x", true, quiet)
	if err != nil || h.EntityID != "" || !h.WouldCreate {
		t.Fatalf("dry run: %+v %v", h, err)
	}
	if len(f.srv.Entities()) != 0 {
		t.Fatal("dry run must not write")
	}
	id := f.srv.AddEntity("host", map[string]any{"machine_id": "id-x"}, "")
	h, err = identity.Resolve(context.Background(), f.api, f.target, "id-x", true, quiet)
	if err != nil || h.EntityID != id || h.WouldCreate {
		t.Fatalf("dry run existing: %+v %v", h, err)
	}
}

// FR-012: the create raced with another process → re-search picks it up
// (and FR-013 if two now exist).
func TestResolve_CreateRace(t *testing.T) {
	f := setup(t)
	var once sync.Once
	f.srv.Before = func(r *http.Request) {
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/entities/template/") {
			once.Do(func() { f.srv.AddEntityLocked("host", map[string]any{"machine_id": "race"}, "2026-09-19T00:00:00Z") })
		}
	}
	h, err := identity.Resolve(context.Background(), f.api, f.target, "race", false, quiet)
	if err != nil {
		t.Fatal(err)
	}
	ents := f.srv.Entities()
	if len(ents) != 2 || h.EntityID != ents[0].ID || len(h.Duplicates) != 2 {
		t.Fatalf("host %+v entities %+v", h, ents)
	}
}

// Errors are surfaced, never retried with a different payload (edge cases).
func TestResolve_Errors(t *testing.T) {
	f := setup(t)
	f.srv.FailNext(omnitest.Fault{Method: "POST", PathPrefix: "/entities/search/", Status: 500})
	if _, err := identity.Resolve(context.Background(), f.api, f.target, "e", false, quiet); err == nil {
		t.Fatal("search failure must surface")
	}
	f.srv.FailNext(omnitest.Fault{Method: "POST", PathPrefix: "/entities/template/", Status: 422,
		Body: `{"title":"Validation Failed","status":422,"errors":{"attributes.machine_id":["Nope."]}}`})
	_, err := identity.Resolve(context.Background(), f.api, f.target, "e", false, quiet)
	if !errors.Is(err, omni.ErrValidation) || !strings.Contains(err.Error(), "attributes.machine_id") {
		t.Fatalf("422 must surface field errors: %v", err)
	}
	if len(f.srv.Entities()) != 0 {
		t.Fatal("nothing created")
	}
	if _, err := identity.Resolve(context.Background(), f.api, f.target, "", false, quiet); err == nil {
		t.Fatal("empty identity must be rejected")
	}
}
