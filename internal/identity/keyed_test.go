package identity_test

import (
	"context"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/identity"
)

func keyedSetup(t *testing.T) (fixture, *identity.Keyed, string) {
	t.Helper()
	f := setup(t)
	f.srv.AddAttribute("gadget_host", "reference")
	f.srv.AddAttribute("gadget_name", "string")
	f.srv.AddTemplate("gadget", "Gadget", "gadget_host", "gadget_name")
	host := f.srv.AddEntity("host", map[string]any{"machine_id": "id-1"}, "")
	k := &identity.Keyed{API: f.api, HostID: host, HostLink: map[string]string{"gadget": "gadget_host"}, Log: quiet}
	return f, k, host
}

// spec 011 US-1/1-2, US-2/1, FR-011, FR-013: the first resolution creates the
// entity holding the key with only its host link; later ones are cached.
func TestKeyed_CreateLinkAndCache(t *testing.T) {
	f, k, host := keyedSetup(t)
	ctx := context.Background()
	id, outcome, err := k.Resolve(ctx, "gadget", "gadget", "SN-1")
	if err != nil || outcome != identity.Created || id == "" {
		t.Fatalf("resolve: %s %s %v", id, outcome, err)
	}
	var e struct{ key, link any }
	for _, x := range f.srv.Entities() {
		if x.ID == id {
			e.key, e.link = x.ExternalKey, x.Values["gadget_host"]
			if len(x.Values) != 1 {
				t.Fatalf("created with more than the host link: %v", x.Values)
			}
		}
	}
	if e.key != "SN-1" || e.link != host {
		t.Fatalf("entity key/link = %v / %v", e.key, e.link)
	}

	f.srv.ResetRequests()
	again, outcome, err := k.Resolve(ctx, "gadget", "gadget", "SN-1")
	if err != nil || again != id || outcome != identity.Found || len(f.srv.Requests()) != 0 {
		t.Fatalf("cached resolve: %s %s %v, %d requests", again, outcome, err, len(f.srv.Requests()))
	}

	// A restarted process finds the same entity (US-1/2).
	k2 := &identity.Keyed{API: f.api, HostID: host, HostLink: k.HostLink, Log: quiet}
	if id2, outcome, err := k2.Resolve(ctx, "gadget", "gadget", "SN-1"); err != nil || id2 != id || outcome != identity.Found {
		t.Fatalf("after restart: %s %s %v", id2, outcome, err)
	}
}

// spec 011 US-4/3, FR-015: after Forget, a deleted entity is created again.
func TestKeyed_ForgetRecreatesDeleted(t *testing.T) {
	f, k, _ := keyedSetup(t)
	ctx := context.Background()
	id, _, _ := k.Resolve(ctx, "gadget", "gadget", "SN-1")
	f.srv.DeleteEntity(id)
	k.Forget("gadget", "SN-1")
	id2, outcome, err := k.Resolve(ctx, "gadget", "gadget", "SN-1")
	if err != nil || id2 == id || outcome != identity.Created {
		t.Fatalf("recreate: %s %s %v", id2, outcome, err)
	}
}

// spec 011 FR-020: dry-run looks up but never creates.
func TestKeyed_DryRun(t *testing.T) {
	f, k, _ := keyedSetup(t)
	k.DryRun = true
	id, outcome, err := k.Resolve(context.Background(), "gadget", "gadget", "SN-1")
	if err != nil || id != "" || outcome != identity.WouldCreate {
		t.Fatalf("dry-run: %s %s %v", id, outcome, err)
	}
	if len(f.srv.Entities()) != 1 {
		t.Fatalf("dry-run created: %+v", f.srv.Entities())
	}
}
