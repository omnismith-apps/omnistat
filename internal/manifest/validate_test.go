package manifest_test

import (
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

func valid() manifest.Manifest {
	return manifest.Manifest{
		Module: "cpu",
		Attributes: []manifest.Attribute{
			{Key: "model", Name: "CPU model", Slug: "cpu_model", Kind: manifest.KindText, Description: "Model string"},
			{Key: "load_5m", Name: "Load 5m", Slug: "cpu_load_5m", Kind: manifest.KindMetric, Description: "5 minute load"},
			{Key: "arch", Name: "Architecture", Slug: "cpu_arch", Kind: manifest.KindList, Options: []string{"amd64", "arm64"}},
		},
	}
}

func TestValidate_OK(t *testing.T) {
	if err := manifest.Validate([]manifest.Manifest{valid()}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// FR-001, FR-002, FR-004, FR-005: every error class is reported, all at once.
func TestValidate_ReportsEveryProblem(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m *manifest.Manifest)
		wantSub string
	}{
		{"empty module name", func(m *manifest.Manifest) { m.Module = "" }, "module name"},
		{"bad module name", func(m *manifest.Manifest) { m.Module = "CPU Stuff" }, `module name "CPU Stuff"`},
		{"empty key", func(m *manifest.Manifest) { m.Attributes[0].Key = "" }, "attribute key"},
		{"duplicate key", func(m *manifest.Manifest) { m.Attributes[1].Key = "model" }, `duplicate attribute key "model"`},
		{"empty name", func(m *manifest.Manifest) { m.Attributes[0].Name = "" }, `"model": name`},
		{"bad slug uppercase", func(m *manifest.Manifest) { m.Attributes[0].Slug = "CpuModel" }, `slug "CpuModel"`},
		{"bad slug leading digit", func(m *manifest.Manifest) { m.Attributes[0].Slug = "1cpu" }, `slug "1cpu"`},
		{"bad slug dash", func(m *manifest.Manifest) { m.Attributes[0].Slug = "cpu-model" }, `slug "cpu-model"`},
		{"unknown kind", func(m *manifest.Manifest) { m.Attributes[0].Kind = "blob" }, `kind "blob"`},
		{"reference kind rejected", func(m *manifest.Manifest) { m.Attributes[0].Kind = "reference" }, `kind "reference"`},
		{"file kind rejected", func(m *manifest.Manifest) { m.Attributes[0].Kind = "file" }, `kind "file"`},
		{"list without options", func(m *manifest.Manifest) { m.Attributes[2].Options = nil }, "list attribute needs options"},
		{"non-list with options", func(m *manifest.Manifest) { m.Attributes[0].Options = []string{"x"} }, "only list attributes may have options"},
		{"duplicate option", func(m *manifest.Manifest) { m.Attributes[2].Options = []string{"amd64", "amd64"} }, `duplicate option "amd64"`},
		{"empty option", func(m *manifest.Manifest) { m.Attributes[2].Options = []string{"amd64", " "} }, "empty option"},
		{"bad template slug", func(m *manifest.Manifest) { m.Attributes[0].Template = "Host-Template" }, `template slug "Host-Template"`},
		{"bad declared template", func(m *manifest.Manifest) {
			m.Templates = []manifest.Template{{Slug: "Bad Slug", Name: "x"}}
		}, `template slug "Bad Slug"`},
		{"declared template without name", func(m *manifest.Manifest) {
			m.Templates = []manifest.Template{{Slug: "disk", Name: ""}}
		}, `template "disk": name`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := valid()
			tt.mutate(&m)
			err := manifest.Validate([]manifest.Manifest{m})
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestValidate_AllAtOnce(t *testing.T) {
	m := valid()
	m.Attributes[0].Slug = "Bad"
	m.Attributes[1].Name = ""
	err := manifest.Validate([]manifest.Manifest{m})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{`slug "Bad"`, `"load_5m": name`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// FR-002: default slugs are unique across all modules.
func TestValidate_CrossModuleDuplicates(t *testing.T) {
	a := valid()
	b := valid()
	b.Module = "memory"
	b.Attributes = []manifest.Attribute{{Key: "model", Name: "x", Slug: "cpu_model", Kind: manifest.KindText}}
	err := manifest.Validate([]manifest.Manifest{a, b})
	if err == nil || !strings.Contains(err.Error(), `slug "cpu_model" declared by both "cpu" and "memory"`) {
		t.Fatalf("expected cross-module duplicate error, got %v", err)
	}
	c := valid()
	c.Module = "cpu"
	err = manifest.Validate([]manifest.Manifest{a, c})
	if err == nil || !strings.Contains(err.Error(), `duplicate module "cpu"`) {
		t.Fatalf("expected duplicate module error, got %v", err)
	}
}

func TestKind_Valid(t *testing.T) {
	for _, k := range []manifest.Kind{manifest.KindText, manifest.KindNumber, manifest.KindBoolean, manifest.KindDate, manifest.KindDatetime, manifest.KindList, manifest.KindMetric} {
		if !k.Valid() {
			t.Errorf("%q should be valid", k)
		}
	}
	if manifest.Kind("reference").Valid() {
		t.Error("reference must not be a valid manifest kind (FR-005)")
	}
}

func TestHostTemplate(t *testing.T) {
	if manifest.HostTemplate != "host" || manifest.HostTemplateName != "Host" {
		t.Fatalf("FR-003: host template constant changed: %q/%q", manifest.HostTemplate, manifest.HostTemplateName)
	}
}
