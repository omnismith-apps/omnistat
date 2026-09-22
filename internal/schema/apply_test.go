package schema_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

var quiet = slog.New(slog.DiscardHandler)

func client(t *testing.T, srv *omnitest.Server) *omni.Client {
	t.Helper()
	c, err := omni.New(omni.Settings{BaseURL: srv.URL, Token: omnitest.Token, ProjectID: omnitest.ProjectID, Retries: 0})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustRead(t *testing.T, api schema.API) schema.Current {
	t.Helper()
	cur, err := api.ReadSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return cur
}

func doneTypes(r schema.Result) string {
	var out []string
	for _, d := range r.Done {
		s := d.Action.Type.String() + ":" + d.Action.Slug()
		if d.Skipped {
			s += "(skipped)"
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

// US-1/2, FR-023, FR-025: full apply on an empty project, then no-op (US-1/3).
func TestApply_EmptyProject(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	api := client(t, srv)
	ctx := context.Background()

	res, err := schema.Apply(ctx, api, desired(), mustRead(t, api), quiet)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "create_template:host create_attribute:cpu_arch create_attribute:cpu_model create_attribute:cpu_usage add_list_option:cpu_arch add_list_option:cpu_arch"
	if got := doneTypes(res); got != want {
		t.Fatalf("done:\n got %s\nwant %s", got, want)
	}
	tpls := srv.Templates()
	if len(tpls) != 1 || strings.Join(tpls[0].AttributeSlugs, ",") != "cpu_arch,cpu_model,cpu_usage" {
		t.Fatalf("server templates: %+v", tpls)
	}
	if a := srv.Attributes()[0]; strings.Join(a.Options, ",") != "amd64,arm64" {
		t.Fatalf("options: %+v", a)
	}
	if res.Resolved.Templates["host"] != tpls[0].ID || res.Resolved.Attributes["cpu_model"] == "" || len(res.Resolved.Attributes) != 3 {
		t.Fatalf("resolved: %+v", res.Resolved)
	}
	// Spec 003 FR-012a: options created in this run have ids in Resolved.
	if items := res.Resolved.ListItems["cpu_arch"]; len(items) != 2 || items["arm64"] != srv.Attributes()[0].OptionIDs["arm64"] || items["arm64"] == "" {
		t.Fatalf("list items: %+v", res.Resolved.ListItems)
	}
	if res.Rereads != 0 {
		t.Fatalf("no re-read expected, got %d", res.Rereads)
	}

	n := len(srv.Requests())
	res, err = schema.Apply(ctx, api, desired(), mustRead(t, api), quiet)
	if err != nil || len(res.Done) != 0 {
		t.Fatalf("second apply should be a no-op: err=%v done=%v", err, doneTypes(res))
	}
	if len(srv.Requests()) != n+1 { // exactly the schema read made by mustRead
		t.Fatalf("no-op apply must not write; requests %d → %d", n, len(srv.Requests()))
	}
}

// FR-022 / US-3/3: any conflict → nothing written.
func TestApply_ConflictPreflight(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("cpu_model", "number")
	api := client(t, srv)

	before := len(srv.Requests())
	_, err := schema.Apply(context.Background(), api, desired(), mustRead(t, api), quiet)
	var ce *schema.ConflictError
	if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].Slug != "cpu_model" {
		t.Fatalf("want ConflictError, got %v", err)
	}
	if !strings.Contains(err.Error(), "cpu_model") {
		t.Fatalf("message: %v", err)
	}
	if len(srv.Requests()) != before+1 {
		t.Fatalf("conflict must not write; requests %d → %d", before, len(srv.Requests()))
	}
}

// FR-024: another actor creates the same attribute between our read and write
// → re-read, treat as existing, continue; final state converges.
func TestApply_RaceObjectAppeared(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	api := client(t, srv)
	cur := mustRead(t, api)

	var once sync.Once
	srv.Before = func(r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/attributes" {
			once.Do(func() {
				// the other host created cpu_model first (unbound)
				srv.AddAttributeLocked("cpu_model", "string")
			})
		}
	}
	res, err := schema.Apply(context.Background(), api, desired(), cur, quiet)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Rereads != 1 {
		t.Fatalf("rereads: %d", res.Rereads)
	}
	if got := doneTypes(res); !strings.Contains(got, "create_attribute:cpu_model(skipped)") {
		t.Fatalf("expected the raced create to be skipped: %s", got)
	}
	// exactly one cpu_model; it ends up bound to host (re-diff produced a bind)
	n := 0
	for _, a := range srv.Attributes() {
		if a.Slug == "cpu_model" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("cpu_model count %d", n)
	}
	slugs := append([]string(nil), srv.Templates()[0].AttributeSlugs...)
	sort.Strings(slugs)
	if got := strings.Join(slugs, ","); got != "cpu_arch,cpu_model,cpu_usage" {
		t.Fatalf("host bindings: %s", got)
	}
	if len(res.Resolved.Attributes) != 3 {
		t.Fatalf("resolved: %+v", res.Resolved)
	}
}

// FR-024: "already exists" but the object is NOT there after re-read → the
// original error is surfaced, not an infinite loop.
func TestApply_RaceObjectAbsent(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	api := client(t, srv)
	cur := mustRead(t, api)
	srv.FailNext(omnitest.Fault{Method: "POST", PathPrefix: "/templates", Status: 409, Body: `{"title":"Conflict","status":409}`})

	res, err := schema.Apply(context.Background(), api, desired(), cur, quiet)
	if !errors.Is(err, schema.ErrAlreadyExists) {
		t.Fatalf("want original already-exists error, got %v", err)
	}
	if res.Failed == nil || res.Failed.Action.Type != schema.CreateTemplate || res.Rereads != 1 {
		t.Fatalf("result: failed=%+v rereads=%d", res.Failed, res.Rereads)
	}
	if len(srv.Templates()) != 0 {
		t.Fatal("nothing should have been created")
	}
}

// FR-026: a hard failure stops the run with a partial report; re-running is
// safe and completes the rest.
func TestApply_PartialFailureThenResume(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	api := client(t, srv)
	cur := mustRead(t, api)
	srv.FailNext(omnitest.Fault{Method: "POST", PathPrefix: "/attributes", Status: 500})

	res, err := schema.Apply(context.Background(), api, desired(), cur, quiet)
	if err == nil || res.Failed == nil || res.Failed.Action.Attribute != "cpu_arch" {
		t.Fatalf("expected failure on cpu_arch: err=%v failed=%+v", err, res.Failed)
	}
	if got := doneTypes(res); got != "create_template:host" {
		t.Fatalf("done before failure: %s", got)
	}
	var ae *omni.APIError
	if !errors.As(err, &ae) || ae.Status != 500 {
		t.Fatalf("error should carry the API error: %v", err)
	}

	res, err = schema.Apply(context.Background(), api, desired(), mustRead(t, api), quiet)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := doneTypes(res); got != "create_attribute:cpu_arch create_attribute:cpu_model create_attribute:cpu_usage add_list_option:cpu_arch add_list_option:cpu_arch" {
		t.Fatalf("resume done: %s", got)
	}
}

// US-2/2, FR-017: bind an existing attribute; existing bindings preserved; a
// verification read follows binds and the resolved map is complete.
func TestApply_BindPreservesAndVerifies(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("cpu_usage", "metric")
	srv.AddAttribute("other", "string")
	srv.AddTemplate("server", "Server", "cpu_usage", "other")
	api := client(t, srv)
	cur := mustRead(t, api)

	d := desired()
	d.Attributes = d.Attributes[2:] // only cpu_usage, on host
	res, err := schema.Apply(context.Background(), api, d, cur, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := doneTypes(res); got != "create_template:host bind_attribute:cpu_usage" {
		t.Fatalf("done: %s", got)
	}
	for _, tpl := range srv.Templates() {
		if !strings.Contains(strings.Join(tpl.AttributeSlugs, ","), "cpu_usage") {
			t.Fatalf("cpu_usage should be bound to %s: %+v", tpl.Slug, tpl)
		}
	}
	reads := 0
	for _, r := range srv.Requests() {
		if r.Path == "/discovery/project-schema" {
			reads++
		}
	}
	if reads != 2 { // mustRead + verification after binds
		t.Fatalf("schema reads: %d", reads)
	}
	if res.Resolved.Templates["host"] == "" || res.Resolved.Attributes["cpu_usage"] == "" {
		t.Fatalf("resolved: %+v", res.Resolved)
	}
}

// FR-024 for options: the option appears between read and write → skipped.
func TestApply_OptionsRace(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("cpu_arch", "list", "amd64")
	srv.AddAttribute("cpu_model", "string")
	srv.AddAttribute("cpu_usage", "metric")
	srv.AddTemplate("host", "Host", "cpu_arch", "cpu_model", "cpu_usage")
	api := client(t, srv)
	cur := mustRead(t, api)

	var once sync.Once
	srv.Before = func(r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/items") {
			once.Do(func() { srv.AddOptionLocked("cpu_arch", "arm64") })
		}
	}
	res, err := schema.Apply(context.Background(), api, desired(), cur, quiet)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := doneTypes(res); got != "add_list_option:cpu_arch(skipped)" || res.Rereads != 1 {
		t.Fatalf("done: %s rereads=%d", got, res.Rereads)
	}
	if a := srv.Attributes()[0]; strings.Join(a.Options, ",") != "amd64,arm64" {
		t.Fatalf("options: %v", a.Options)
	}
}
