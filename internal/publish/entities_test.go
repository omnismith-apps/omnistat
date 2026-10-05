package publish_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/publish"
)

type entityFixture struct {
	srv  *omnitest.Server
	host string
	ents *publish.Entities
	bufs *collect.Buffers
	logs *bytes.Buffer
}

var sn1 = collect.Target{Module: "gadget", Template: "gadget", Key: "SN-1"}
var sn2 = collect.Target{Module: "gadget", Template: "gadget", Key: "SN-2"}

func newEntityFixture(t *testing.T, dryRun bool) *entityFixture {
	t.Helper()
	srv := omnitest.New()
	t.Cleanup(srv.Close)
	srv.AddAttribute("machine_id", "string")
	srv.AddAttribute("gadget_count", "number")
	srv.AddTemplate("host", "Host", "machine_id", "gadget_count")
	srv.AddReference("gadget_host", "host", "machine_id")
	srv.AddAttribute("gadget_name", "string")
	srv.AddAttribute("gadget_level_pct", "metric")
	srv.AddTemplate("gadget", "Gadget", "gadget_host", "gadget_name", "gadget_level_pct")
	host := srv.AddEntity("host", map[string]any{"machine_id": "id-1"}, "")
	api, err := omni.New(omni.Settings{BaseURL: srv.URL, Token: omnitest.Token, ProjectID: omnitest.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	links := map[string]string{"gadget": "gadget_host"}
	ents := &publish.Entities{
		Host:     &publish.Publisher{API: api, EntityID: host, Log: log},
		Resolver: &identity.Keyed{API: api, HostID: host, HostLink: links, DryRun: dryRun, Log: log},
		HostLink: links,
		Log:      log,
	}
	return &entityFixture{srv: srv, host: host, ents: ents, bufs: collect.NewBuffers(0, log), logs: &logs}
}

func (f *entityFixture) add(target collect.Target, slug string, kind manifest.Kind, v any) {
	f.bufs.Add(collect.Sample{Module: "gadget", Key: slug, Slug: slug, Kind: kind, Value: v, At: t0, Target: target})
}

func (f *entityFixture) byKey(key string) *omnitest.EntitySnapshot {
	for _, e := range f.srv.Entities() {
		if e.ExternalKey == key {
			return &e
		}
	}
	return nil
}

// spec 011 US-1/1, US-2/1, FR-016…FR-018: the host and each entity get their
// own values; every entity's dimension update carries the host link.
func TestEntities_PublishesEachEntity(t *testing.T) {
	f := newEntityFixture(t, false)
	f.add(collect.Target{}, "gadget_count", manifest.KindNumber, 2.0)
	f.add(sn1, "gadget_name", manifest.KindText, "first")
	f.add(sn1, "gadget_level_pct", manifest.KindMetric, 50.0)
	f.add(sn2, "gadget_level_pct", manifest.KindMetric, 70.0)

	res, err := f.ents.Publish(context.Background(), f.bufs)
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, f.logs)
	}
	if res.Entities != 2 || res.EntitiesFailed != 0 || res.Dimensions != 3 || res.Observations != 2 {
		t.Fatalf("result: %+v", res)
	}
	if v := f.srv.EntityValues(f.host); v["gadget_count"] != "2" {
		t.Fatalf("host values: %v", v)
	}
	e1 := f.byKey("SN-1")
	if e1 == nil || e1.Values["gadget_name"] != "first" || e1.Values["gadget_host"] != f.host {
		t.Fatalf("SN-1: %+v", e1)
	}
	if m := f.srv.EntityMetrics(e1.ID); len(m["gadget_level_pct"]) != 1 {
		t.Fatalf("SN-1 metrics: %v", m)
	}
	e2 := f.byKey("SN-2")
	if e2 == nil || len(f.srv.EntityMetrics(e2.ID)["gadget_level_pct"]) != 1 {
		t.Fatalf("SN-2: %+v", e2)
	}
	if !f.bufs.Empty() {
		t.Fatal("acknowledged samples still buffered")
	}
	// A metric-only publish does not rewrite the link (no dimension update).
	before := len(f.srv.EntityHistory(e2.ID))
	f.add(sn2, "gadget_level_pct", manifest.KindMetric, 71.0)
	if _, err := f.ents.Publish(context.Background(), f.bufs); err != nil {
		t.Fatal(err)
	}
	if after := len(f.srv.EntityHistory(e2.ID)); after != before {
		t.Fatalf("metric-only publish wrote dimensions: %d → %d", before, after)
	}
}

// spec 011 US-4/3, FR-015: an entity deleted mid-run is created again at the
// next publish, and its values are kept until then.
func TestEntities_DeletedEntityIsRecreated(t *testing.T) {
	f := newEntityFixture(t, false)
	f.add(sn1, "gadget_name", manifest.KindText, "first")
	if _, err := f.ents.Publish(context.Background(), f.bufs); err != nil {
		t.Fatal(err)
	}
	gone := f.byKey("SN-1").ID
	f.srv.DeleteEntity(gone)

	f.add(sn1, "gadget_name", manifest.KindText, "second")
	res, err := f.ents.Publish(context.Background(), f.bufs)
	if err != nil || res.EntitiesFailed != 1 {
		t.Fatalf("publish after delete: %+v %v", res, err)
	}
	if f.bufs.For(sn1).Empty() {
		t.Fatal("values of a vanished entity were dropped")
	}
	if !strings.Contains(f.logs.String(), "created again") {
		t.Fatalf("no warning:\n%s", f.logs)
	}
	res, err = f.ents.Publish(context.Background(), f.bufs)
	if err != nil || res.Entities != 1 {
		t.Fatalf("publish: %+v %v", res, err)
	}
	if e := f.byKey("SN-1"); e == nil || e.ID == gone || e.Values["gadget_name"] != "second" {
		t.Fatalf("recreated: %+v", e)
	}
}

// spec 011 FR-014, FR-017: a failing entity does not stop the others.
func TestEntities_FailureIsIsolated(t *testing.T) {
	f := newEntityFixture(t, false)
	f.add(sn1, "gadget_name", manifest.KindText, "first")
	f.add(sn2, "gadget_name", manifest.KindText, "second")
	f.srv.FailNext(omnitest.Fault{Method: "GET", PathPrefix: "/entities/template/gadget/by-key", Status: 500})
	res, err := f.ents.Publish(context.Background(), f.bufs)
	if err != nil || res.Entities != 1 || res.EntitiesFailed != 1 {
		t.Fatalf("publish: %+v %v\n%s", res, err, f.logs)
	}
	if f.byKey("SN-2") == nil || f.bufs.For(sn1).Empty() {
		t.Fatal("the healthy entity must be written and the failed one kept")
	}
}

// spec 011 FR-019: a 422 drops what the platform names, on that entity only.
func TestEntities_RejectedDimensionDropped(t *testing.T) {
	f := newEntityFixture(t, false)
	f.add(sn1, "gadget_name", manifest.KindText, "first")
	f.add(sn1, "gadget_ghost", manifest.KindText, "boo") // not in the project
	res, err := f.ents.Publish(context.Background(), f.bufs)
	if err != nil || res.Entities != 1 || res.Dropped != 1 {
		t.Fatalf("publish: %+v %v\n%s", res, err, f.logs)
	}
	if e := f.byKey("SN-1"); e.Values["gadget_name"] != "first" {
		t.Fatalf("entity: %+v", e)
	}
}

// spec 011 FR-020: dry-run prints each entity with its template, key and
// whether it would be created, and writes nothing.
func TestEntities_DryRun(t *testing.T) {
	f := newEntityFixture(t, true)
	var out bytes.Buffer
	f.ents.Host.Printer = &publish.Printer{W: &out, JSON: true}
	f.add(collect.Target{}, "gadget_count", manifest.KindNumber, 1.0)
	f.add(sn1, "gadget_name", manifest.KindText, "first")
	if _, err := f.ents.Publish(context.Background(), f.bufs); err != nil {
		t.Fatal(err)
	}
	if len(f.srv.Entities()) != 1 {
		t.Fatalf("dry-run created entities: %+v", f.srv.Entities())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one document per entity:\n%s", out.String())
	}
	var doc struct {
		Entity, Template, Key string
		Create                bool `json:"would_create"`
		Dimensions            []struct{ Slug, Value string }
	}
	if err := json.Unmarshal([]byte(lines[1]), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Template != "gadget" || doc.Key != "SN-1" || !doc.Create || doc.Entity != "" || len(doc.Dimensions) != 2 || doc.Dimensions[1].Slug != "gadget_host" {
		t.Fatalf("entity document: %+v", doc)
	}

	out.Reset()
	f.ents.Host.Printer.JSON = false
	f.add(sn1, "gadget_name", manifest.KindText, "again")
	if _, err := f.ents.Publish(context.Background(), f.bufs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `would publish to gadget "SN-1" (to be created)`) {
		t.Fatalf("text:\n%s", out.String())
	}
}
