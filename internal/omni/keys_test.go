package omni_test

import (
	"context"
	"errors"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

// spec 011 FR-011, FR-012: the keyed operations against the fake, including
// a record with no values, whose empty attribute_values SDK v1.0.18 cannot
// decode.
func TestClient_KeyedEntities(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("machine_id", "string")
	srv.AddTemplate("host", "Host", "machine_id")
	c, err := omni.New(omni.Settings{BaseURL: srv.URL, Token: omnitest.Token, ProjectID: omnitest.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, found, err := c.EntityByKey(ctx, "host", "k1"); err != nil || found {
		t.Fatalf("missing key: found=%v err=%v", found, err)
	}
	bare := srv.AddEntity("host", map[string]any{}, "")
	if err := c.SetEntityKey(ctx, bare, "k1"); err != nil {
		t.Fatal(err)
	}
	if id, found, err := c.EntityByKey(ctx, "host", "k1"); err != nil || !found || id != bare {
		t.Fatalf("record without values: %s %v %v", id, found, err)
	}

	id, created, err := c.UpsertByKey(ctx, "host", "k2", map[string]any{"machine_id": "k2"})
	if err != nil || !created || id == "" {
		t.Fatalf("upsert create: %s %v %v", id, created, err)
	}
	again, created, err := c.UpsertByKey(ctx, "host", "k2", map[string]any{})
	if err != nil || created || again != id {
		t.Fatalf("upsert existing: %s %v %v", again, created, err)
	}
	if err := c.SetEntityKey(ctx, bare, "k2"); !errors.Is(err, identity.ErrKeyTaken) {
		t.Fatalf("set a held key: want ErrKeyTaken, got %v", err)
	}
	found, err := c.FindEntities(ctx, srv.TemplateID("host"), "machine_id", "k2")
	if err != nil || len(found) != 1 || found[0].Key != "k2" {
		t.Fatalf("search with keys: %+v %v", found, err)
	}
}
