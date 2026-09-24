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

// TestSandbox_Resolve runs the real Resolve against a real project. It needs
// OMNISMITH_ACCESS_TOKEN / OMNISMITH_PROJECT_ID / OMNISMITH_BASE_URL and a
// reconciled schema (`omnistat schema apply`). It creates one host entity
// with a throwaway identity and never deletes it (constitution IV).
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
	value := "sandbox-" + time.Now().UTC().Format("20060102T150405")
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// dry-run: nothing yet
	h, err := identity.Resolve(ctx, api, target, value, true, log)
	if err != nil || !h.WouldCreate {
		t.Fatalf("dry-run: %+v %v", h, err)
	}
	// first run creates
	h1, err := identity.Resolve(ctx, api, target, value, false, log)
	if err != nil || !h1.Created || h1.EntityID == "" {
		t.Fatalf("create: %+v %v", h1, err)
	}
	t.Logf("created entity %s for identity %s", h1.EntityID, value)
	// second run reuses: Resolve waited until its create was searchable
	// (writes are processed asynchronously, 002 FR-012), so an immediate
	// second resolution finds it instead of creating a duplicate
	h2, err := identity.Resolve(ctx, api, target, value, false, log)
	if err != nil || h2.Created || h2.EntityID != h1.EntityID {
		t.Fatalf("reuse: %+v %v", h2, err)
	}
	// exact match: a prefix/superstring identity is not the same host
	h3, err := identity.Resolve(ctx, api, target, value+"-x", true, log)
	if err != nil || !h3.WouldCreate {
		t.Fatalf("exact match: %+v %v", h3, err)
	}
	h4, err := identity.Resolve(ctx, api, target, value[:len(value)-3], true, log)
	if err != nil || !h4.WouldCreate {
		t.Fatalf("exact match (prefix): %+v %v", h4, err)
	}
}
