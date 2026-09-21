package schema_test

import (
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

func desired() manifest.Desired {
	return manifest.Desired{
		Templates: []manifest.DesiredTemplate{{Slug: "host", Name: "Host", Description: "A machine"}},
		Attributes: []manifest.DesiredAttribute{
			{Slug: "cpu_arch", Name: "Arch", Kind: manifest.KindList, Options: []string{"amd64", "arm64"}, Templates: []string{"host"}, Module: "cpu", Key: "arch"},
			{Slug: "cpu_model", Name: "CPU model", Kind: manifest.KindText, Description: "d", Templates: []string{"host"}, Module: "cpu", Key: "model"},
			{Slug: "cpu_usage", Name: "CPU usage", Kind: manifest.KindMetric, Templates: []string{"host"}, Module: "cpu", Key: "usage"},
		},
	}
}

func kinds(p schema.Plan) string {
	var out []string
	for _, a := range p.Actions {
		out = append(out, a.Type.String()+":"+a.Slug())
	}
	return strings.Join(out, " ")
}

// US-1/1: empty project → create template, attributes, options; bindings ride on creation.
func TestDiff_EmptyProject(t *testing.T) {
	p := schema.Diff(desired(), schema.Current{})
	if len(p.Conflicts) != 0 {
		t.Fatalf("conflicts: %+v", p.Conflicts)
	}
	want := "create_template:host create_attribute:cpu_arch create_attribute:cpu_model create_attribute:cpu_usage add_list_option:cpu_arch add_list_option:cpu_arch"
	if got := kinds(p); got != want {
		t.Fatalf("plan:\n got %s\nwant %s", got, want)
	}
	a := p.Actions[1]
	if a.Template != "" || len(a.Templates) != 1 || a.Templates[0] != "host" || a.Kind != manifest.KindList || a.Module != "cpu" {
		t.Fatalf("create_attribute action: %+v", a)
	}
	if p.Actions[4].Option != "amd64" || p.Actions[5].Option != "arm64" {
		t.Fatalf("options must keep manifest order: %+v", p.Actions[4:])
	}
	if p.Actions[0].Name != "Host" || p.Actions[0].Description != "A machine" {
		t.Fatalf("create_template carries name/description: %+v", p.Actions[0])
	}
	if p.Empty() {
		t.Fatal("plan should not be empty")
	}
}

// US-1/3, FR-014, FR-018: everything present (with different names) → no changes.
func TestDiff_NoChanges(t *testing.T) {
	cur := schema.Current{
		Templates: map[string]schema.CurrentTemplate{
			"host": {ID: "t1", Slug: "host", Name: "Renamed Host", AttributeIDs: []string{"a1", "a2", "a3"}},
		},
		Attributes: map[string]schema.CurrentAttribute{
			"cpu_arch":  {ID: "a1", Slug: "cpu_arch", Name: "Whatever", Type: "list", Options: []string{"arm64", "amd64", "riscv"}},
			"cpu_model": {ID: "a2", Slug: "cpu_model", Name: "x", Type: "string"},
			"cpu_usage": {ID: "a3", Slug: "cpu_usage", Name: "y", Type: "metric"},
		},
	}
	p := schema.Diff(desired(), cur)
	if !p.Empty() || len(p.Conflicts) != 0 {
		t.Fatalf("expected empty plan, got %s conflicts=%+v", kinds(p), p.Conflicts)
	}
}

// US-2/2, FR-017: attribute exists elsewhere → bind, preserving existing bindings.
func TestDiff_BindExisting(t *testing.T) {
	cur := schema.Current{
		Templates: map[string]schema.CurrentTemplate{
			"host":   {ID: "t1", Slug: "host", AttributeIDs: []string{"a1", "a2"}},
			"server": {ID: "t2", Slug: "server", AttributeIDs: []string{"a3", "a9"}},
		},
		Attributes: map[string]schema.CurrentAttribute{
			"cpu_arch":  {ID: "a1", Slug: "cpu_arch", Type: "list", Options: []string{"amd64", "arm64"}},
			"cpu_model": {ID: "a2", Slug: "cpu_model", Type: "string"},
			"cpu_usage": {ID: "a3", Slug: "cpu_usage", Type: "metric"},
			"other":     {ID: "a9", Slug: "other", Type: "string"},
		},
	}
	p := schema.Diff(desired(), cur)
	if got := kinds(p); got != "bind_attribute:cpu_usage" {
		t.Fatalf("plan: %s", got)
	}
	b := p.Actions[0]
	if b.Template != "host" || b.AttributeID != "a3" {
		t.Fatalf("bind action: %+v", b)
	}
	// existing binding (server) must be preserved, target (host) added
	if strings.Join(b.TemplateIDs, ",") != "t1,t2" {
		t.Fatalf("bind must carry existing ∪ target template ids, got %v", b.TemplateIDs)
	}
}

// FR-015: kind and data-type conflicts; FR-016: options add / ignore extra / case-sensitive.
func TestDiff_ConflictsAndOptions(t *testing.T) {
	cur := schema.Current{
		Templates: map[string]schema.CurrentTemplate{"host": {ID: "t1", Slug: "host", AttributeIDs: []string{"a1", "a2", "a3"}}},
		Attributes: map[string]schema.CurrentAttribute{
			"cpu_arch":  {ID: "a1", Slug: "cpu_arch", Type: "list", Options: []string{"AMD64"}},
			"cpu_model": {ID: "a2", Slug: "cpu_model", Type: "number"},
			"cpu_usage": {ID: "a3", Slug: "cpu_usage", Type: "string"},
		},
	}
	p := schema.Diff(desired(), cur)
	if got := kinds(p); got != "add_list_option:cpu_arch add_list_option:cpu_arch" {
		t.Fatalf("plan: %s", got)
	}
	if p.Actions[0].Option != "amd64" || p.Actions[0].AttributeID != "a1" {
		t.Fatalf("option action: %+v", p.Actions[0])
	}
	if len(p.Conflicts) != 2 {
		t.Fatalf("conflicts: %+v", p.Conflicts)
	}
	c := p.Conflicts[0]
	if c.Slug != "cpu_model" || c.Module != "cpu" || c.Expected != "text" || c.Actual != "number" {
		t.Fatalf("conflict: %+v", c)
	}
	if p.Conflicts[1].Slug != "cpu_usage" || p.Conflicts[1].Expected != "metric" || p.Conflicts[1].Actual != "string" {
		t.Fatalf("conflict: %+v", p.Conflicts[1])
	}
	if !strings.Contains(c.Error(), "cpu_model") || !strings.Contains(c.Error(), "cpu") {
		t.Fatalf("conflict message: %s", c.Error())
	}
}

// FR-019: deterministic order regardless of input order.
func TestDiff_Deterministic(t *testing.T) {
	d := desired()
	d.Templates = append(d.Templates, manifest.DesiredTemplate{Slug: "disk", Name: "Disk"})
	d.Attributes = append(d.Attributes, manifest.DesiredAttribute{Slug: "aa_first", Name: "a", Kind: manifest.KindText, Templates: []string{"disk", "host"}, Module: "m", Key: "k"})
	// reverse the input
	for i, j := 0, len(d.Attributes)-1; i < j; i, j = i+1, j-1 {
		d.Attributes[i], d.Attributes[j] = d.Attributes[j], d.Attributes[i]
	}
	p := schema.Diff(d, schema.Current{})
	want := "create_template:disk create_template:host create_attribute:aa_first create_attribute:cpu_arch create_attribute:cpu_model create_attribute:cpu_usage add_list_option:cpu_arch add_list_option:cpu_arch"
	if got := kinds(p); got != want {
		t.Fatalf("plan:\n got %s\nwant %s", got, want)
	}
	if strings.Join(p.Actions[2].Templates, ",") != "disk,host" {
		t.Fatalf("multi-template create must list templates sorted: %v", p.Actions[2].Templates)
	}
}

// A mix: existing template missing one binding for a NEW attribute must not
// produce a bind (creation binds); an existing attribute bound to none → bind.
func TestDiff_NewAttributeOnExistingTemplate(t *testing.T) {
	cur := schema.Current{
		Templates:  map[string]schema.CurrentTemplate{"host": {ID: "t1", Slug: "host"}},
		Attributes: map[string]schema.CurrentAttribute{"cpu_model": {ID: "a2", Slug: "cpu_model", Type: "string"}},
	}
	p := schema.Diff(desired(), cur)
	want := "create_attribute:cpu_arch create_attribute:cpu_usage add_list_option:cpu_arch add_list_option:cpu_arch bind_attribute:cpu_model"
	if got := kinds(p); got != want {
		t.Fatalf("plan:\n got %s\nwant %s", got, want)
	}
	if b := p.Actions[4]; strings.Join(b.TemplateIDs, ",") != "t1" {
		t.Fatalf("bind ids: %v", b.TemplateIDs)
	}
}

func TestDiff_KindMapping(t *testing.T) {
	for kind, typ := range map[manifest.Kind]string{
		manifest.KindText: "string", manifest.KindNumber: "number", manifest.KindBoolean: "boolean",
		manifest.KindDate: "date", manifest.KindDatetime: "datetime", manifest.KindList: "list", manifest.KindMetric: "metric",
	} {
		if got := schema.TypeOf(kind); got != typ {
			t.Errorf("TypeOf(%s) = %s, want %s", kind, got, typ)
		}
	}
}
