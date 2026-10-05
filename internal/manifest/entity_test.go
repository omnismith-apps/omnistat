package manifest_test

import (
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// gadget is a module with one entity template, its host link and two values;
// label is a host-template module whose attribute a host link can show.
func gadget() manifest.Manifest {
	return manifest.Manifest{
		Module:    "gadget",
		Templates: []manifest.Template{{Slug: "gadget", Name: "Gadget", Entity: true}},
		Attributes: []manifest.Attribute{
			{Key: "host", Name: "Managed by", Slug: "gadget_host", Kind: manifest.KindReference, Template: "gadget", Target: manifest.HostTemplate},
			{Key: "name", Name: "Gadget name", Slug: "gadget_name", Kind: manifest.KindText, Template: "gadget"},
			{Key: "level", Name: "Gadget level", Slug: "gadget_level_pct", Kind: manifest.KindMetric, Template: "gadget"},
		},
	}
}

func labels() manifest.Manifest {
	return manifest.Manifest{
		Module: "names",
		Attributes: []manifest.Attribute{
			{Key: "id", Name: "Identity", Slug: "names_id", Kind: manifest.KindText, Label: 1},
			{Key: "host", Name: "Host name", Slug: "names_host", Kind: manifest.KindText, Label: 2},
			{Key: "other", Name: "Other", Slug: "names_other", Kind: manifest.KindText},
		},
	}
}

// spec 011 FR-001, FR-002: entity templates and host links are validated.
func TestValidate_EntityTemplates(t *testing.T) {
	if err := manifest.Validate([]manifest.Manifest{gadget(), labels()}); err != nil {
		t.Fatalf("valid entity template rejected: %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(m *manifest.Manifest)
		wantSub string
	}{
		{"no host link", func(m *manifest.Manifest) { m.Attributes = m.Attributes[1:] }, `entity template "gadget" needs exactly one host link`},
		{"two host links", func(m *manifest.Manifest) {
			m.Attributes = append(m.Attributes, manifest.Attribute{Key: "host2", Name: "Again", Slug: "gadget_host2", Kind: manifest.KindReference, Template: "gadget", Target: manifest.HostTemplate})
		}, `has 2`},
		{"reference to another template", func(m *manifest.Manifest) { m.Attributes[0].Target = "gadget" }, `may only target the host template`},
		{"reference on the host", func(m *manifest.Manifest) { m.Attributes[0].Template = "" }, `must sit on an entity template`},
		{"target on a non-reference", func(m *manifest.Manifest) { m.Attributes[1].Target = manifest.HostTemplate }, `only references may have a target`},
		{"host as entity template", func(m *manifest.Manifest) {
			m.Templates[0].Slug = manifest.HostTemplate
			for i := range m.Attributes {
				m.Attributes[i].Template = manifest.HostTemplate
			}
		}, `host template cannot be an entity template`},
		{"negative label", func(m *manifest.Manifest) { m.Attributes[1].Label = -1 }, `label rank`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := gadget()
			tt.mutate(&m)
			err := manifest.Validate([]manifest.Manifest{m})
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("want error containing %q, got %v", tt.wantSub, err)
			}
		})
	}
}

// spec 011 FR-001, FR-003: the entity template is desired as such, its
// attributes know it, and the host link shows the highest-ranked label.
func TestResolve_EntityTemplate(t *testing.T) {
	d, err := manifest.Resolve([]manifest.Manifest{labels(), gadget()}, manifest.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	tpl, ok := d.EntityTemplate("gadget", "gadget")
	if !ok || tpl.Slug != "gadget" || !tpl.Entity || tpl.Name != "Gadget" {
		t.Fatalf("entity template = %+v, %v", tpl, ok)
	}
	if d.Host != manifest.HostTemplate {
		t.Fatalf("host = %q", d.Host)
	}
	link, ok := d.HostLink("gadget")
	if !ok || link.Target != "host" || link.Display != "names_host" || link.EntityTemplate != "gadget" {
		t.Fatalf("host link = %+v, %v", link, ok)
	}
	level, _ := d.Find("gadget", "level")
	if level.EntityTemplate != "gadget" || level.Templates[0] != "gadget" {
		t.Fatalf("level = %+v", level)
	}
	host, _ := d.Find("names", "host")
	if host.EntityTemplate != "" {
		t.Fatalf("host attribute marked as entity: %+v", host)
	}
}

// spec 011 FR-003: without the rank-2 label the identity (rank 1) is shown;
// a label moved off the host template does not count.
func TestResolve_HostLinkDisplayFallsBack(t *testing.T) {
	l := labels()
	l.Attributes = []manifest.Attribute{l.Attributes[0], l.Attributes[2]}
	d, err := manifest.Resolve([]manifest.Manifest{l, gadget()}, manifest.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if link, _ := d.HostLink("gadget"); link.Display != "names_id" {
		t.Fatalf("display = %q, want names_id", link.Display)
	}

	ov := manifest.Overrides{Modules: map[string]manifest.ModuleOverride{"names": {Attributes: map[string]manifest.AttributeOverride{"host": {Template: "elsewhere"}}}}}
	d, err = manifest.Resolve([]manifest.Manifest{labels(), gadget()}, ov)
	if err != nil {
		t.Fatal(err)
	}
	if link, _ := d.HostLink("gadget"); link.Display != "names_id" {
		t.Fatalf("display = %q, want names_id once names_host left the host template", link.Display)
	}

	_, err = manifest.Resolve([]manifest.Manifest{gadget()}, manifest.Overrides{})
	if err == nil || !strings.Contains(err.Error(), "no label attribute") {
		t.Fatalf("want missing-label error, got %v", err)
	}
}

// spec 011 FR-005: a module-level override renames the entity template and
// its attributes follow; a host template override moves the link's target.
func TestResolve_EntityTemplateOverrides(t *testing.T) {
	ov := manifest.Overrides{
		HostTemplate: "server",
		Modules:      map[string]manifest.ModuleOverride{"gadget": {Template: "power_device"}},
	}
	d, err := manifest.Resolve([]manifest.Manifest{labels(), gadget()}, ov)
	if err != nil {
		t.Fatal(err)
	}
	if _, declared := d.Template("gadget"); declared {
		t.Fatal("the renamed entity template is still declared under its manifest slug")
	}
	tpl, ok := d.EntityTemplate("gadget", "gadget")
	if !ok || tpl.Slug != "power_device" || tpl.Name != "Gadget" {
		t.Fatalf("entity template = %+v", tpl)
	}
	link, _ := d.HostLink("power_device")
	if link.Target != "server" {
		t.Fatalf("link target = %q, want server", link.Target)
	}
	if a, _ := d.Find("gadget", "name"); a.Templates[0] != "power_device" || a.EntityTemplate != "gadget" {
		t.Fatalf("name = %+v", a)
	}
}

// spec 011 FR-005: the overrides an entity template forbids.
func TestResolve_EntityTemplateOverrideErrors(t *testing.T) {
	mixed := gadget()
	mixed.Attributes = append(mixed.Attributes, manifest.Attribute{Key: "count", Name: "Gadgets", Slug: "gadget_count", Kind: manifest.KindNumber})
	other := manifest.Manifest{
		Module:     "other",
		Templates:  []manifest.Template{{Slug: "widget", Name: "Widget", Entity: true}},
		Attributes: []manifest.Attribute{{Key: "host", Name: "Managed by", Slug: "widget_host", Kind: manifest.KindReference, Template: "widget", Target: manifest.HostTemplate}},
	}
	tests := []struct {
		name string
		ms   []manifest.Manifest
		ov   manifest.Overrides
		want string
	}{
		{"attribute moved off its entity template", []manifest.Manifest{labels(), gadget()},
			manifest.Overrides{Modules: map[string]manifest.ModuleOverride{"gadget": {Attributes: map[string]manifest.AttributeOverride{"level": {Template: "host"}}}}},
			`belongs to entity template "gadget" and cannot be moved`},
		{"entity template onto the host", []manifest.Manifest{labels(), gadget()},
			manifest.Overrides{Modules: map[string]manifest.ModuleOverride{"gadget": {Template: "host"}}},
			`cannot be the host template`},
		{"module override on a mixed module", []manifest.Manifest{labels(), mixed},
			manifest.Overrides{Modules: map[string]manifest.ModuleOverride{"gadget": {Template: "power_device"}}},
			`cannot be moved together`},
		{"two modules on one entity template", []manifest.Manifest{labels(), gadget(), other},
			manifest.Overrides{Modules: map[string]manifest.ModuleOverride{"other": {Template: "gadget"}}},
			`entity template "gadget" is used by both`},
		{"host attribute onto an entity template", []manifest.Manifest{labels(), gadget()},
			manifest.Overrides{Modules: map[string]manifest.ModuleOverride{"names": {Attributes: map[string]manifest.AttributeOverride{"other": {Template: "gadget"}}}}},
			`is module "gadget"'s entity template`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := manifest.Resolve(tt.ms, tt.ov)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}
