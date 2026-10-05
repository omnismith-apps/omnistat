package schema_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

// withLink is desired() plus an entity template whose host link sorts before
// the display attribute it names, so ordering is actually exercised.
func withLink() manifest.Desired {
	d := desired()
	d.Host = "host"
	d.Templates = append(d.Templates, manifest.DesiredTemplate{Slug: "gadget", Name: "Gadget", Entity: true, Module: "gadget", Key: "gadget"})
	d.Attributes = append([]manifest.DesiredAttribute{
		{Slug: "a_gadget_host", Name: "Managed by", Kind: manifest.KindReference, Templates: []string{"gadget"}, Module: "gadget", Key: "host",
			EntityTemplate: "gadget", Target: "host", Display: "cpu_model"},
	}, d.Attributes...)
	return d
}

// spec 011 FR-003, 001 FR-019: a missing host link is created after every
// other attribute, carrying its target and display attribute.
func TestDiff_ReferenceCreatedAfterOtherAttributes(t *testing.T) {
	p := schema.Diff(withLink(), schema.Current{})
	want := "create_template:gadget create_template:host create_attribute:cpu_arch create_attribute:cpu_model create_attribute:cpu_usage create_attribute:a_gadget_host add_list_option:cpu_arch add_list_option:cpu_arch"
	if got := kinds(p); got != want {
		t.Fatalf("plan:\n got %s\nwant %s", got, want)
	}
	ref := p.Actions[5]
	if ref.Target != "host" || ref.Display != "cpu_model" || ref.Kind != manifest.KindReference {
		t.Fatalf("reference action: %+v", ref)
	}
	if !strings.Contains(p.Text(), "+ attribute a_gadget_host (reference → host) → gadget  [gadget]") {
		t.Fatalf("plan text:\n%s", p.Text())
	}
	out, err := p.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Actions []map[string]any `json:"actions"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Actions[5]["target"] != "host" || doc.Actions[5]["display"] != "cpu_model" {
		t.Fatalf("JSON reference action: %v", doc.Actions[5])
	}
}

// spec 011 FR-004: an existing host link matches when it points at the host
// template, whatever it displays; another target or another kind conflicts.
func TestDiff_ExistingReference(t *testing.T) {
	cur := func(refTarget string, typ string) schema.Current {
		return schema.Current{
			Templates: map[string]schema.CurrentTemplate{
				"host":   {ID: "t-host", Slug: "host"},
				"gadget": {ID: "t-gadget", Slug: "gadget", AttributeIDs: []string{"a-link"}},
				"other":  {ID: "t-other", Slug: "other"},
			},
			Attributes: map[string]schema.CurrentAttribute{
				"a_gadget_host": {ID: "a-link", Slug: "a_gadget_host", Type: typ, RefTemplateID: refTarget},
			},
		}
	}
	p := schema.Diff(withLink(), cur("t-host", "reference"))
	for _, a := range p.Actions {
		if a.Slug() == "a_gadget_host" {
			t.Fatalf("matching host link planned again: %+v", a)
		}
	}
	if len(p.Conflicts) != 0 {
		t.Fatalf("conflicts: %+v", p.Conflicts)
	}

	p = schema.Diff(withLink(), cur("t-other", "reference"))
	if len(p.Conflicts) != 1 || p.Conflicts[0].Expected != "reference → host" || p.Conflicts[0].Actual != "reference → other" {
		t.Fatalf("want a reference-target conflict, got %+v", p.Conflicts)
	}
	p = schema.Diff(withLink(), cur("", "string"))
	if len(p.Conflicts) != 1 || p.Conflicts[0].Expected != "reference" || p.Conflicts[0].Actual != "string" {
		t.Fatalf("want a kind conflict, got %+v", p.Conflicts)
	}
}

// spec 011 FR-003: apply creates the reference with the ids the platform
// requires, and the fake reports its target back.
func TestApply_CreatesReference(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	api := client(t, srv)
	ctx := context.Background()
	if _, err := schema.Apply(ctx, api, withLink(), mustRead(t, api), quiet); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if target, display := srv.AttributeReference("a_gadget_host"); target != "host" || display != "cpu_model" {
		t.Fatalf("reference = %q / %q", target, display)
	}
	if p := schema.Diff(withLink(), mustRead(t, api)); !p.Empty() {
		t.Fatalf("second plan not empty: %s", p.Text())
	}
}
