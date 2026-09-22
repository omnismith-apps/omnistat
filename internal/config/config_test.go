package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Defaults(t *testing.T) {
	s, err := config.Load("", env(map[string]string{"OMNISMITH_ACCESS_TOKEN": "omni_t", "OMNISMITH_PROJECT_ID": "p1"}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Token != "omni_t" || s.ProjectID != "p1" || s.BaseURL != "https://api.omnismith.io/v1" {
		t.Fatalf("settings: %+v", s)
	}
	if s.Mode != config.ModeApply || s.Overrides.HostTemplate != "" || s.HTTP.Timeout != 15*time.Second || s.HTTP.Retries != 3 {
		t.Fatalf("defaults: %+v", s)
	}
	if s.Log.Level != "info" || s.Log.Format != "text" || len(s.Modules) != 0 {
		t.Fatalf("defaults: %+v", s)
	}
	if s.PublishInterval != 60*time.Second || len(s.Intervals) != 0 {
		t.Fatalf("interval defaults: %+v", s)
	}
}

// FR-006…010: every knob from YAML; env wins for project/base URL; token only from env.
func TestLoad_Full(t *testing.T) {
	s, err := config.Load("testdata/full.yaml", env(map[string]string{"OMNISMITH_ACCESS_TOKEN": "omni_t"}))
	if err != nil {
		t.Fatal(err)
	}
	if s.ProjectID != "01a0c47a-0000-7000-8000-000000000001" || s.BaseURL != "https://api.example.test/v1" {
		t.Fatalf("ids: %+v", s)
	}
	if s.Mode != config.ModeVerify || s.Overrides.HostTemplate != "server" {
		t.Fatalf("schema: %+v", s)
	}
	cpu := s.Overrides.Modules["cpu"]
	if cpu.Template != "node" || cpu.Attributes["usage"].Slug != "cpu_usage" || cpu.Attributes["usage"].Template != "server" || cpu.Attributes["usage"].Name != "CPU %" || cpu.Attributes["usage"].Description != "Renamed by the operator" {
		t.Fatalf("cpu override: %+v", cpu)
	}
	if on, ok := s.Modules["cpu"]; !ok || !on {
		t.Fatalf("cpu switch: %v %v", on, ok)
	}
	if on, ok := s.Modules["disk"]; !ok || on {
		t.Fatalf("disk switch: %v %v", on, ok)
	}
	if s.HTTP.Timeout != 5*time.Second || s.HTTP.Retries != 1 || s.Log.Level != "debug" || s.Log.Format != "json" {
		t.Fatalf("http/log: %+v", s)
	}
	// Spec 003 FR-003/FR-013: intervals.
	if s.PublishInterval != 30*time.Second || s.Intervals["hostname"] != 10*time.Minute || len(s.Intervals) != 1 {
		t.Fatalf("intervals: %+v", s)
	}

	// env overrides file
	s, err = config.Load("testdata/full.yaml", env(map[string]string{"OMNISMITH_ACCESS_TOKEN": "t", "OMNISMITH_PROJECT_ID": "p2", "OMNISMITH_BASE_URL": "http://localhost:8100"}))
	if err != nil || s.ProjectID != "p2" || s.BaseURL != "http://localhost:8100" {
		t.Fatalf("env precedence: %+v %v", s, err)
	}
}

func TestLoad_Errors(t *testing.T) {
	ok := env(map[string]string{"OMNISMITH_ACCESS_TOKEN": "t"})
	tests := []struct {
		name, path string
		env        func(string) string
		want       []string
	}{
		{"missing file", "testdata/nope.yaml", ok, []string{"nope.yaml"}},
		{"unknown key", "testdata/unknown_key.yaml", ok, []string{"schmea"}},
		{"token in file", "testdata/token_in_file.yaml", ok, []string{"access_token", "must not be in the config file"}},
		{"invalid values", "testdata/invalid.yaml", ok, []string{`schema.mode "sometimes"`, `schema.host_template "Host Template"`, "http.timeout", "http.retries", `log.level "loud"`, `log.format "xml"`,
			"publish.interval: 2h0m0s is outside 1s…1h0m0s", "modules.cpu.interval: 500ms is outside 1s…24h0m0s", `modules.ram.interval: "soon"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(tt.path, tt.env)
			if err == nil {
				t.Fatal("expected error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error should mention %q: %v", w, err)
				}
			}
		})
	}
}

// Missing token/project are not Load errors (version needs neither); the
// caller asks for them when a command needs the API (IV: token only from env).
func TestSettings_RequireAPI(t *testing.T) {
	s, err := config.Load("testdata/minimal.yaml", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequireAPI(); err == nil || !strings.Contains(err.Error(), "OMNISMITH_ACCESS_TOKEN") {
		t.Fatalf("want token error, got %v", err)
	}
	s, _ = config.Load("", env(map[string]string{"OMNISMITH_ACCESS_TOKEN": "t"}))
	if err := s.RequireAPI(); err == nil || !strings.Contains(err.Error(), "OMNISMITH_PROJECT_ID") {
		t.Fatalf("want project error, got %v", err)
	}
	s, _ = config.Load("", env(map[string]string{"OMNISMITH_ACCESS_TOKEN": "t", "OMNISMITH_PROJECT_ID": "p"}))
	if err := s.RequireAPI(); err != nil {
		t.Fatal(err)
	}
}

// The default config file is picked up when present and no path is given.
func TestLoad_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := config.Load("", env(nil)); err != nil {
		t.Fatalf("no file, no path must be fine: %v", err)
	}
	if err := writeFile(dir+"/omnistat.yaml", "project_id: from_default_file\n"); err != nil {
		t.Fatal(err)
	}
	s, err := config.Load("", env(nil))
	if err != nil || s.ProjectID != "from_default_file" || s.Path != "omnistat.yaml" {
		t.Fatalf("default path: %+v %v", s, err)
	}
}

// Spec 002 FR-008/009: static identity from file, env wins, validation.
func TestLoad_Identity(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/id.yaml"
	if err := writeFile(path, "identity:\n  static: \"  rack7-node3 \"\n"); err != nil {
		t.Fatal(err)
	}
	s, err := config.Load(path, env(nil))
	if err != nil || s.Identity != "rack7-node3" {
		t.Fatalf("file: %q %v", s.Identity, err)
	}
	s, err = config.Load(path, env(map[string]string{"OMNISTAT_IDENTITY": "from-env"}))
	if err != nil || s.Identity != "from-env" {
		t.Fatalf("env wins: %q %v", s.Identity, err)
	}
	s, err = config.Load("", env(nil))
	if err != nil || s.Identity != "" {
		t.Fatalf("default: %q %v", s.Identity, err)
	}
	if _, err := config.Load("", env(map[string]string{"OMNISTAT_IDENTITY": "   "})); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("whitespace env: %v", err)
	}
	if err := writeFile(path, "identity:\n  static: \" \"\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path, env(nil)); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("whitespace file: %v", err)
	}
	if _, err := config.Load("", env(map[string]string{"OMNISTAT_IDENTITY": strings.Repeat("x", 129)})); err == nil || !strings.Contains(err.Error(), "maximum is 128") {
		t.Fatalf("too long: %v", err)
	}
}

// Spec 001 FR-006 / US-4/1: disabling a module must work. A `modules.<name>`
// block that only switches the module on or off is not a schema override, and
// recording it as one made `enabled: false` fail with "override for unknown
// module" — the module is, by then, deliberately not among the manifests.
func TestLoad_SwitchesAreNotSchemaOverrides(t *testing.T) {
	path := t.TempDir() + "/omnistat.yaml"
	if err := writeFile(path, "modules:\n  cpu:\n    enabled: false\n  hostname:\n    interval: 30s\n"); err != nil {
		t.Fatal(err)
	}
	s, err := config.Load(path, env(map[string]string{"OMNISMITH_ACCESS_TOKEN": "omni_t", "OMNISMITH_PROJECT_ID": "p1"}))
	if err != nil {
		t.Fatal(err)
	}
	if on, set := s.Modules["cpu"]; !set || on {
		t.Fatalf("cpu should be switched off: %v %v", on, set)
	}
	if got := s.Intervals["hostname"]; got != 30*time.Second {
		t.Fatalf("hostname interval: %v", got)
	}
	if len(s.Overrides.Modules) != 0 {
		t.Fatalf("switches must not become schema overrides: %+v", s.Overrides.Modules)
	}
	// A block that does remap schema still produces an override.
	path2 := t.TempDir() + "/omnistat.yaml"
	if err := writeFile(path2, "modules:\n  cpu:\n    enabled: true\n    attributes:\n      usage: { slug: busy_pct }\n"); err != nil {
		t.Fatal(err)
	}
	s2, err := config.Load(path2, env(map[string]string{"OMNISMITH_ACCESS_TOKEN": "omni_t", "OMNISMITH_PROJECT_ID": "p1"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Overrides.Modules["cpu"].Attributes["usage"].Slug; got != "busy_pct" {
		t.Fatalf("real overrides must survive: %q", got)
	}
}
