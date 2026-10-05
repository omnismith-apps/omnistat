//go:build sandbox

package identity_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/omni"
)

// TestSandbox_Resolve runs the real ResolveHost against a real project. It
// needs OMNISMITH_ACCESS_TOKEN / OMNISMITH_PROJECT_ID / OMNISMITH_BASE_URL and
// a reconciled schema (`omnistat schema apply`). It creates host entities with
// throwaway identities and never deletes them (constitution IV).
//
//	go test -tags sandbox -run TestSandbox_Resolve -v ./internal/identity/
func TestSandbox_Resolve(t *testing.T) {
	s, err := config.Load("", os.Getenv)
	if err != nil || s.RequireAPI() != nil {
		t.Skip("sandbox credentials not set")
	}
	api, err := omni.New(omni.Settings{BaseURL: s.BaseURL, Token: s.Token, ProjectID: s.ProjectID, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cur, err := api.ReadSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	host, ok := cur.Templates["host"]
	if !ok || cur.Attributes["machine_id"].ID == "" {
		t.Fatal("schema not reconciled: run `omnistat schema apply`")
	}
	target := identity.Target{TemplateSlug: "host", TemplateID: host.ID, AttributeSlug: "machine_id"}
	stamp := time.Now().UTC().Format("20060102T150405")
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// A fresh identity: dry-run, create, then an immediate lookup finds it
	// (spec 011: the key does not lag behind the write).
	value := "sandbox-" + stamp
	if h, err := identity.ResolveHost(ctx, api, target, value, true, log); err != nil || h.Outcome != identity.WouldCreate {
		t.Fatalf("dry-run: %+v %v", h, err)
	}
	h1, err := identity.ResolveHost(ctx, api, target, value, false, log)
	if err != nil || h1.Outcome != identity.Created || h1.EntityID == "" {
		t.Fatalf("create: %+v %v", h1, err)
	}
	h2, err := identity.ResolveHost(ctx, api, target, value, false, log)
	if err != nil || h2.Outcome != identity.Found || h2.EntityID != h1.EntityID {
		t.Fatalf("immediate lookup: %+v %v", h2, err)
	}
	// Exact match: a superstring is not the same host.
	if h, err := identity.ResolveHost(ctx, api, target, value+"-x", true, log); err != nil || h.Outcome != identity.WouldCreate {
		t.Fatalf("exact match: %+v %v", h, err)
	}

	// A legacy host entity (machine_id, no key) is adopted, not duplicated
	// (spec 011 US-3/1, NFR-006).
	legacyValue := "sandbox-legacy-" + stamp
	legacy, err := api.CreateEntity(ctx, "host", map[string]any{"machine_id": legacyValue})
	if err != nil {
		t.Fatal(err)
	}
	// The record must be searchable before adoption can find it (writes are
	// processed asynchronously); wait for that, then resolve once.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		found, err := api.FindEntities(ctx, host.ID, "machine_id", legacyValue)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("legacy entity never became searchable")
		}
	}
	adopted, err := identity.ResolveHost(ctx, api, target, legacyValue, false, log)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Outcome != identity.Adopted || adopted.EntityID != legacy {
		t.Fatalf("adopt: %+v (legacy %s)", adopted, legacy)
	}
	if again, err := identity.ResolveHost(ctx, api, target, legacyValue, false, log); err != nil || again.Outcome != identity.Found || again.EntityID != legacy {
		t.Fatalf("after adoption: %+v %v", again, err)
	}
}
