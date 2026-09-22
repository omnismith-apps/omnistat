package manifest_test

import (
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

func two() []manifest.Manifest {
	return []manifest.Manifest{
		{Module: "cpu", Attributes: []manifest.Attribute{
			{Key: "model", Name: "CPU model", Slug: "cpu_model", Kind: manifest.KindText, Description: "d1"},
			{Key: "usage", Name: "CPU usage", Slug: "cpu_usage_pct", Kind: manifest.KindMetric},
		}},
		{Module: "disk", Templates: []manifest.Template{{Slug: "disk", Name: "Disk", Description: "One per mount"}},
			Attributes: []manifest.Attribute{
				{Key: "mount", Name: "Mount point", Slug: "disk_mount", Kind: manifest.KindText, Template: "disk"},
				{Key: "kind", Name: "FS kind", Slug: "disk_fs", Kind: manifest.KindList, Options: []string{"ext4", "xfs"}, Template: "disk"},
				{Key: "count", Name: "Disks", Slug: "disk_count", Kind: manifest.KindNumber},
			}},
	}
}

func attr(t *testing.T, d manifest.Desired, slug string) manifest.DesiredAttribute {
	t.Helper()
	for _, a := range d.Attributes {
		if a.Slug == slug {
			return a
		}
	}
	t.Fatalf("attribute %q not in desired state: %+v", slug, d.Attributes)
	return manifest.DesiredAttribute{}
}

// FR-003, FR-011: defaults land on `host`; declared templates are carried; union is sorted.
func TestResolve_Defaults(t *testing.T) {
	d, err := manifest.Resolve(two(), manifest.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Templates) != 2 || d.Templates[0].Slug != "disk" || d.Templates[1].Slug != "host" || d.Templates[1].Name != "Host" {
		t.Fatalf("templates: %+v", d.Templates)
	}
	if got := attr(t, d, "cpu_model"); got.Templates[0] != "host" || got.Module != "cpu" || got.Key != "model" || got.Description != "d1" {
		t.Fatalf("cpu_model: %+v", got)
	}
	if got := attr(t, d, "disk_mount"); got.Templates[0] != "disk" {
		t.Fatalf("disk_mount should bind to disk: %+v", got)
	}
	if got := attr(t, d, "disk_count"); got.Templates[0] != "host" {
		t.Fatalf("disk_count should bind to host: %+v", got)
	}
	for i := 1; i < len(d.Attributes); i++ {
		if d.Attributes[i-1].Slug >= d.Attributes[i].Slug {
			t.Fatalf("attributes not sorted: %v", d.Attributes)
		}
	}
}

// FR-007: attribute override > module override > manifest default; FR-008 name/description.
func TestResolve_OverridePrecedence(t *testing.T) {
	ov := manifest.Overrides{
		HostTemplate: "server",
		Modules: map[string]manifest.ModuleOverride{
			"cpu": {Template: "node", Attributes: map[string]manifest.AttributeOverride{
				"usage": {Slug: "cpu_usage", Template: "server", Name: "CPU %", Description: "renamed"},
			}},
		},
	}
	d, err := manifest.Resolve(two(), ov)
	if err != nil {
		t.Fatal(err)
	}
	// module override wins over the host default for cpu_model
	if got := attr(t, d, "cpu_model"); got.Templates[0] != "node" {
		t.Fatalf("cpu_model should bind to node: %+v", got)
	}
	// attribute override wins over module override; slug/name/description applied
	got := attr(t, d, "cpu_usage")
	if got.Templates[0] != "server" || got.Name != "CPU %" || got.Description != "renamed" || got.Key != "usage" {
		t.Fatalf("cpu_usage: %+v", got)
	}
	// global host template override applies to attributes with no other override
	if got := attr(t, d, "disk_count"); got.Templates[0] != "server" {
		t.Fatalf("disk_count should bind to server: %+v", got)
	}
	// templates: server (host, renamed slug keeps Host name), node, disk
	slugs := []string{}
	for _, tpl := range d.Templates {
		slugs = append(slugs, tpl.Slug)
	}
	if strings.Join(slugs, ",") != "disk,node,server" {
		t.Fatalf("templates: %v", slugs)
	}
}

// FR-009: collisions after overrides, invalid override slugs, unknown keys are fatal.
func TestResolve_Errors(t *testing.T) {
	tests := []struct {
		name string
		ov   manifest.Overrides
		want string
	}{
		{"slug collision across modules", manifest.Overrides{Modules: map[string]manifest.ModuleOverride{
			"disk": {Attributes: map[string]manifest.AttributeOverride{"count": {Slug: "cpu_model"}}}}},
			`slug "cpu_model" is mapped by both cpu/model and disk/count`},
		{"slug collision within module", manifest.Overrides{Modules: map[string]manifest.ModuleOverride{
			"cpu": {Attributes: map[string]manifest.AttributeOverride{"usage": {Slug: "cpu_model"}}}}},
			`slug "cpu_model" is mapped by both cpu/model and cpu/usage`},
		{"bad override slug", manifest.Overrides{Modules: map[string]manifest.ModuleOverride{
			"cpu": {Attributes: map[string]manifest.AttributeOverride{"usage": {Slug: "CPU-usage"}}}}},
			`slug "CPU-usage"`},
		{"bad host template", manifest.Overrides{HostTemplate: "My Host"}, `template slug "My Host"`},
		{"bad module template", manifest.Overrides{Modules: map[string]manifest.ModuleOverride{"cpu": {Template: "x y"}}}, `template slug "x y"`},
		{"unknown module", manifest.Overrides{Modules: map[string]manifest.ModuleOverride{"gpu": {}}}, `unknown module "gpu"`},
		{"unknown attribute key", manifest.Overrides{Modules: map[string]manifest.ModuleOverride{
			"cpu": {Attributes: map[string]manifest.AttributeOverride{"temp": {Slug: "t"}}}}},
			`module "cpu" has no attribute "temp"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := manifest.Resolve(two(), tt.ov)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

// FR-011: two modules attaching to the same non-host template share it; the
// template is declared once, with the first declaration's name.
func TestResolve_SharedTemplate(t *testing.T) {
	ms := two()
	ms[0].Attributes[0].Template = "disk"
	d, err := manifest.Resolve(ms, manifest.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, tpl := range d.Templates {
		if tpl.Slug == "disk" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("disk template declared %d times", n)
	}
	if got := attr(t, d, "cpu_model"); got.Templates[0] != "disk" {
		t.Fatalf("cpu_model should bind to disk: %+v", got)
	}
}

// An attribute remapped to a template nobody declares still yields that template
// (created with a name derived from its slug), so "fit an existing schema" works
// even when the target template does not exist yet.
func TestResolve_UndeclaredTemplateIsCreated(t *testing.T) {
	ov := manifest.Overrides{Modules: map[string]manifest.ModuleOverride{
		"cpu": {Attributes: map[string]manifest.AttributeOverride{"model": {Template: "server_node"}}}}}
	d, err := manifest.Resolve(two(), ov)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tpl := range d.Templates {
		if tpl.Slug == "server_node" {
			found = true
			if tpl.Name != "Server Node" {
				t.Fatalf("derived name: %q", tpl.Name)
			}
		}
	}
	if !found {
		t.Fatalf("server_node template missing: %+v", d.Templates)
	}
}

// FR-017/FR-020 (ADR-0007): an attribute's platform declaration survives Resolve
// untouched by overrides, and the desired schema is the same everywhere — a host
// that cannot collect a value still declares it, so a mixed fleet converges on one
// schema (constitution III).
func TestResolve_PlatformsSurviveAndDoNotFilter(t *testing.T) {
	ms := two()
	ms[0].Attributes[1].Platforms = []string{"linux", "darwin"}

	d, err := manifest.Resolve(ms, manifest.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if got := attr(t, d, "cpu_usage_pct").Platforms; strings.Join(got, ",") != "linux,darwin" {
		t.Fatalf("platforms lost by Resolve: %v", got)
	}
	if got := attr(t, d, "cpu_model").Platforms; len(got) != 0 {
		t.Fatalf("an undeclared platform list must stay empty (= everywhere), got %v", got)
	}

	// A slug override must not disturb the platform list.
	ov := manifest.Overrides{Modules: map[string]manifest.ModuleOverride{
		"cpu": {Attributes: map[string]manifest.AttributeOverride{"usage": {Slug: "busy_pct"}}}}}
	d2, err := manifest.Resolve(ms, ov)
	if err != nil {
		t.Fatal(err)
	}
	if got := attr(t, d2, "busy_pct").Platforms; strings.Join(got, ",") != "linux,darwin" {
		t.Fatalf("platforms lost by an override: %v", got)
	}

	// FR-020: nothing about Resolve depends on the running platform, so the
	// attribute set is identical whatever the host is.
	if len(d.Attributes) != len(two()[0].Attributes)+len(two()[1].Attributes) {
		t.Fatalf("platform-gated attributes must still be desired: %d", len(d.Attributes))
	}
}
