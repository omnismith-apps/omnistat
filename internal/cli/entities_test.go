package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/module/moduletest"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

// withGadget adds the entity-template fixture to the harness, reporting one
// gadget and one host value per collection (spec 011).
func withGadget(h *harness) {
	h.reg.Register(moduletest.WithProvider(moduletest.Gadget(), 0, func(context.Context) ([]module.Observation, error) {
		sn := module.Entity{Template: "gadget", Key: "SN-1"}
		return []module.Observation{
			{Key: "count", Value: 1},
			{Key: "name", Value: "garage", Entity: sn},
			{Key: "on", Value: true, Entity: sn},
			{Key: "level", Value: 42.5, Entity: sn},
		}, nil
	}))
}

func (h *harness) byKey(template, key string) *omnitest.EntitySnapshot {
	for _, e := range h.srv.Entities() {
		if e.TemplateSlug == template && e.ExternalKey == key {
			return &e
		}
	}
	return nil
}

// spec 011 US-1, US-2, FR-010, FR-016…FR-018, NFR-006 (fake): one run creates
// the gadget's own record, linked to the host and shown by its hostname; a
// second run reuses both entities.
func TestRun_ModuleEntity(t *testing.T) {
	h := newHarness(t, "")
	withGadget(h)
	r := h.exec(context.Background(), "run")
	if r.code != 0 || !strings.Contains(r.stdout, "and 1 module entities") {
		t.Fatalf("%+v", r)
	}
	host := h.byKey("host", machineid.Derive(rawID))
	g := h.byKey("gadget", "SN-1")
	if host == nil || g == nil {
		t.Fatalf("entities: %+v", h.srv.Entities())
	}
	if g.Values["gadget_host"] != host.ID || g.Values["gadget_name"] != "garage" || g.Values["gadget_on"] != true {
		t.Fatalf("gadget values: %v", g.Values)
	}
	if host.Values["gadget_count"] != "1" || host.Values["gadget_name"] != nil {
		t.Fatalf("host values: %v", host.Values)
	}
	if m := h.srv.EntityMetrics(g.ID)["gadget_level_pct"]; len(m) != 1 || m[0].Value != "42.5" {
		t.Fatalf("gadget metrics: %+v", m)
	}
	if target, display := h.srv.AttributeReference("gadget_host"); target != "host" || display != "hostname" {
		t.Fatalf("host link = %q shown by %q", target, display)
	}
	if !strings.Contains(h.logsSnapshot(), "entity resolved") {
		t.Fatalf("no resolution record:\n%s", h.logsSnapshot())
	}

	r = h.exec(context.Background(), "run")
	if r.code != 0 || len(h.srv.Entities()) != 2 {
		t.Fatalf("second run: %+v, %d entities", r, len(h.srv.Entities()))
	}
}

// spec 011 FR-020: dry-run shows the gadget as an entity to be created and
// writes no entity.
func TestRun_ModuleEntityDryRun(t *testing.T) {
	h := newHarness(t, "")
	withGadget(h)
	if r := h.exec(context.Background(), "schema", "apply"); r.code != 0 {
		t.Fatalf("apply: %+v", r)
	}
	r := h.exec(context.Background(), "run", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, `would publish to gadget "SN-1" (to be created)`) || !strings.Contains(r.stdout, "(host link) → gadget_host") {
		t.Fatalf("%+v", r)
	}
	if len(h.srv.Entities()) != 0 {
		t.Fatalf("dry-run wrote: %+v", h.srv.Entities())
	}
	r = h.exec(context.Background(), "run", "--dry-run", "--json")
	found := false
	for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
		var doc map[string]any
		if json.Unmarshal([]byte(line), &doc) == nil && doc["template"] == "gadget" && doc["key"] == "SN-1" && doc["would_create"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("no gadget document:\n%s", r.stdout)
	}
}

// spec 011 FR-014, FR-021: a gadget that cannot be written makes the run
// partial; the host is still published.
func TestRun_ModuleEntityFailureIsPartial(t *testing.T) {
	h := newHarness(t, "http:\n  retries: 0\n")
	withGadget(h)
	h.srv.FailNext(omnitest.Fault{Method: "GET", PathPrefix: "/entities/template/gadget/by-key", Status: 500, Times: 5})
	r := h.exec(context.Background(), "run")
	if r.code != cli.ExitPartial || !strings.Contains(r.stderr, "1 module entities were not written") {
		t.Fatalf("%+v", r)
	}
	if host := h.byKey("host", machineid.Derive(rawID)); host == nil || host.Values["hostname"] != "edge-fra-01" {
		t.Fatalf("host not published: %+v", host)
	}
}

// spec 011 FR-012, FR-020: the inspection says when the host would be adopted
// and when it holds someone else's key; the next run adopts it.
func TestIdentity_Adoption(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	fsys := fstest.MapFS{"etc/machine-id": {Data: []byte(rawID)}}
	want := machineid.Derive(rawID)
	if r := exec2(t, srv, registryWithMachineID(fsys), "schema", "apply"); r.code != 0 {
		t.Fatalf("apply: %+v", r)
	}
	id := srv.AddEntity("host", map[string]any{"machine_id": want}, "")
	r := runIdentity(t, srv, fsys, nil, "identity", "--json")
	var rep map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil || rep["outcome"] != "would-adopt" || rep["entity_id"] != id {
		t.Fatalf("would adopt: %v %v", err, rep)
	}
	r = runIdentity(t, srv, fsys, nil, "identity")
	if !strings.Contains(r.stdout, "the next run gives it its key") {
		t.Fatalf("text: %s", r.stdout)
	}
	if srv.Entities()[0].ExternalKey != "" {
		t.Fatal("inspection wrote a key")
	}

	srv.SetEntityKey(id, "proxmox:vm-101")
	r = runIdentity(t, srv, fsys, nil, "identity")
	if r.code != 0 || !strings.Contains(r.stdout, `holds the external key "proxmox:vm-101", left unchanged`) {
		t.Fatalf("other key: %+v", r)
	}
}
