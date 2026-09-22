package omni_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

func newClient(t *testing.T, srv *omnitest.Server, token, project string) *omni.Client {
	t.Helper()
	c, err := omni.New(omni.Settings{BaseURL: srv.URL, Token: token, ProjectID: project, Retries: 0, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNew_Validates(t *testing.T) {
	if _, err := omni.New(omni.Settings{Token: "", ProjectID: "p"}); err == nil {
		t.Error("empty token must fail")
	}
	if _, err := omni.New(omni.Settings{Token: "t", ProjectID: " "}); err == nil {
		t.Error("empty project must fail")
	}
}

// FR-012: discovery → Current, including bindings, options and types.
func TestReadSchema(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("cpu_arch", "list", "amd64", "arm64")
	srv.AddAttribute("cpu_usage", "metric")
	srv.AddAttribute("nickname", "string")
	srv.AddTemplate("host", "Host", "cpu_arch", "cpu_usage")

	cur, err := newClient(t, srv, omnitest.Token, omnitest.ProjectID).ReadSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h := cur.Templates["host"]
	if h.ID == "" || h.Name != "Host" || len(h.AttributeIDs) != 2 {
		t.Fatalf("host: %+v", h)
	}
	if a := cur.Attributes["cpu_arch"]; a.Type != "list" || strings.Join(a.Options, ",") != "amd64,arm64" || a.ID != h.AttributeIDs[0] {
		t.Fatalf("cpu_arch: %+v", a)
	}
	// Spec 003 FR-012a: list item ids round-trip from discovery.
	if want := srv.Attributes()[0].OptionIDs; len(want) != 2 || cur.Attributes["cpu_arch"].OptionIDs["arm64"] != want["arm64"] || want["arm64"] == "" {
		t.Fatalf("option ids: got %v want %v", cur.Attributes["cpu_arch"].OptionIDs, want)
	}
	if cur.Attributes["nickname"].OptionIDs != nil {
		t.Fatal("non-list attribute must have no option ids")
	}
	if cur.Attributes["cpu_usage"].Type != "metric" || cur.Attributes["nickname"].Type != "string" {
		t.Fatalf("types: %+v", cur.Attributes)
	}
	// headers reached the server
	req := srv.Requests()[0]
	if req.Method != "GET" || req.Path != "/discovery/project-schema" {
		t.Fatalf("request: %+v", req)
	}
}

// Writes: create template, attribute (bound at creation), option, bind.
func TestWrites(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	c := newClient(t, srv, omnitest.Token, omnitest.ProjectID)
	ctx := context.Background()

	tid, err := c.CreateTemplate(ctx, schema.CreateTemplateParams{Slug: "host", Name: "Host", Description: "d"})
	if err != nil || tid == "" {
		t.Fatalf("create template: %v", err)
	}
	aid, err := c.CreateAttribute(ctx, schema.CreateAttributeParams{Slug: "cpu_arch", Name: "Arch", Kind: manifest.KindList, TemplateIDs: []string{tid}})
	if err != nil || aid == "" {
		t.Fatalf("create attribute: %v", err)
	}
	if err := c.AddListOption(ctx, aid, "amd64"); err != nil {
		t.Fatalf("add option: %v", err)
	}
	mid, err := c.CreateAttribute(ctx, schema.CreateAttributeParams{Slug: "cpu_usage", Name: "Usage", Kind: manifest.KindMetric})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.BindAttribute(ctx, mid, []string{tid}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	tpls := srv.Templates()
	if len(tpls) != 1 || strings.Join(tpls[0].AttributeSlugs, ",") != "cpu_arch,cpu_usage" {
		t.Fatalf("templates: %+v", tpls)
	}
	attrs := srv.Attributes()
	if attrs[0].Type != "list" || strings.Join(attrs[0].Options, ",") != "amd64" || attrs[1].Type != "metric" {
		t.Fatalf("attributes: %+v", attrs)
	}
	for _, r := range srv.Requests() {
		if r.Method == "POST" && r.Path == "/attributes" && strings.Contains(r.Body, `"slug":"cpu_arch"`) {
			if !strings.Contains(r.Body, `"attribute_type":2`) || !strings.Contains(r.Body, `"template_ids":["`+tid+`"]`) {
				t.Fatalf("create attribute payload: %s", r.Body)
			}
		}
	}
}

// FR-024: duplicate slug → schema.ErrAlreadyExists; FR-027 & friends: sentinels.
func TestErrorMapping(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	ctx := context.Background()
	c := newClient(t, srv, omnitest.Token, omnitest.ProjectID)
	srv.AddAttribute("cpu_usage", "metric")

	_, err := c.CreateAttribute(ctx, schema.CreateAttributeParams{Slug: "cpu_usage", Name: "x", Kind: manifest.KindMetric})
	if !errors.Is(err, schema.ErrAlreadyExists) || !errors.Is(err, omni.ErrValidation) {
		t.Fatalf("duplicate slug should map to ErrAlreadyExists+ErrValidation, got %v", err)
	}
	var ae *omni.APIError
	if !errors.As(err, &ae) || ae.Status != 422 || ae.Fields["slug"] == nil {
		t.Fatalf("APIError: %+v", ae)
	}
	if !strings.Contains(err.Error(), "create attribute cpu_usage") || !strings.Contains(err.Error(), "slug: The slug has already been taken.") {
		t.Fatalf("message: %s", err)
	}

	_, err = newClient(t, srv, "wrong", omnitest.ProjectID).ReadSchema(ctx)
	if !errors.Is(err, omni.ErrUnauthorized) {
		t.Fatalf("401 → ErrUnauthorized, got %v", err)
	}
	_, err = newClient(t, srv, omnitest.Token, "other-project").ReadSchema(ctx)
	if !errors.Is(err, omni.ErrForbidden) || errors.Is(err, omni.ErrStaleGrant) {
		t.Fatalf("403 → ErrForbidden, got %v", err)
	}
	srv.FailNext(omnitest.Fault{Status: 403, Body: `{"title":"Forbidden","status":403,"code":"stale_project_grant"}`})
	_, err = c.ReadSchema(ctx)
	if !errors.Is(err, omni.ErrStaleGrant) || !errors.Is(err, omni.ErrForbidden) {
		t.Fatalf("stale grant, got %v", err)
	}
	srv.FailNext(omnitest.Fault{Status: 403, Body: `{"title":"Project Access Denied","status":403,"code":"project_access_denied"}`})
	_, err = c.ReadSchema(ctx)
	if !errors.Is(err, omni.ErrProjectDeny) || !errors.Is(err, omni.ErrForbidden) {
		t.Fatalf("project access denied, got %v", err)
	}
	srv.FailNext(omnitest.Fault{Status: 409, Body: `{"title":"No project","status":409,"code":"no_project_selected"}`})
	_, err = c.ReadSchema(ctx)
	if !errors.Is(err, omni.ErrNoProject) || errors.Is(err, schema.ErrAlreadyExists) {
		t.Fatalf("no project, got %v", err)
	}
	srv.FailNext(omnitest.Fault{Status: 409, Body: `{"title":"Conflict","status":409}`})
	_, err = c.CreateTemplate(ctx, schema.CreateTemplateParams{Slug: "x", Name: "x"})
	if !errors.Is(err, schema.ErrAlreadyExists) {
		t.Fatalf("plain 409 → already exists, got %v", err)
	}
	err = c.AddListOption(ctx, "nope", "v")
	if !errors.Is(err, omni.ErrNotFound) {
		t.Fatalf("404, got %v", err)
	}
	srv.DenyWrites = true
	_, err = c.CreateTemplate(ctx, schema.CreateTemplateParams{Slug: "y", Name: "y"})
	if !errors.Is(err, omni.ErrForbidden) {
		t.Fatalf("denied write → ErrForbidden, got %v", err)
	}
}

func TestMyPermissions(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.SetPermissions("a", "b")
	perms, err := newClient(t, srv, omnitest.Token, omnitest.ProjectID).MyPermissions(context.Background())
	if err != nil || strings.Join(perms, ",") != "a,b" {
		t.Fatalf("perms %v err %v", perms, err)
	}
}

// Transient failures are retried through the SDK path too.
func TestRetryThroughSDK(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.FailNext(omnitest.Fault{Status: http.StatusServiceUnavailable, Times: 2})
	c, err := omni.New(omni.Settings{BaseURL: srv.URL, Token: omnitest.Token, ProjectID: omnitest.ProjectID, Retries: 2, Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadSchema(context.Background()); err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if n := len(srv.Requests()); n != 3 {
		t.Fatalf("requests: %d", n)
	}
}
