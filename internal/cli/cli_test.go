package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/module/moduletest"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

type run struct {
	code   int
	stdout string
	stderr string
}

func exec(t *testing.T, srv *omnitest.Server, cfg string, args ...string) run {
	t.Helper()
	env := map[string]string{}
	if srv != nil {
		env["OMNISMITH_ACCESS_TOKEN"] = omnitest.Token
		env["OMNISMITH_PROJECT_ID"] = omnitest.ProjectID
		env["OMNISMITH_BASE_URL"] = srv.URL
	}
	if cfg != "" {
		path := filepath.Join(t.TempDir(), "omnistat.yaml")
		if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append([]string{"--config", path}, args...)
	}
	app := &cli.App{Registry: moduletest.Registry(), Version: "1.2.3"}
	var out, errb bytes.Buffer
	code := app.Run(context.Background(), args, &out, &errb, func(k string) string { return env[k] })
	return run{code, out.String(), errb.String()}
}

func TestVersionAndUsage(t *testing.T) {
	r := exec(t, nil, "", "version")
	if r.code != 0 || r.stdout != "omnistat 1.2.3\n" {
		t.Fatalf("%+v", r)
	}
	r = exec(t, nil, "")
	if r.code != cli.ExitError || !strings.Contains(r.stderr, "Usage:") || !strings.Contains(r.stderr, "disk, ident, probe") {
		t.Fatalf("%+v", r)
	}
	r = exec(t, nil, "", "bogus")
	if r.code != cli.ExitError || !strings.Contains(r.stderr, `unknown command "bogus"`) {
		t.Fatalf("%+v", r)
	}
	r = exec(t, nil, "", "schema", "nope")
	if r.code != cli.ExitError || !strings.Contains(r.stderr, "unknown subcommand") {
		t.Fatalf("%+v", r)
	}
}

func TestSchema_MissingCredentials(t *testing.T) {
	r := exec(t, nil, "", "schema", "plan")
	if r.code != cli.ExitError || !strings.Contains(r.stderr, "OMNISMITH_ACCESS_TOKEN") {
		t.Fatalf("%+v", r)
	}
}

// US-1 end to end: plan (2) → apply (0) → plan (0) → verify (0).
func TestSchema_PlanApplyVerify(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()

	r := exec(t, srv, "", "schema", "plan")
	if r.code != cli.ExitChanges {
		t.Fatalf("plan: %+v", r)
	}
	for _, want := range []string{"+ template host", "+ attribute probe_model (text) → host  [probe]", "+ attribute ident_id (text) → host  [ident]", "+ option probe_arch: amd64", "7 actions, 0 conflicts"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("plan output lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "disk") {
		t.Errorf("disk is disabled by default and must not appear:\n%s", r.stdout)
	}
	if len(srv.Templates()) != 0 {
		t.Fatal("plan must not write")
	}

	r = exec(t, srv, "", "schema", "verify")
	if r.code != cli.ExitError || !strings.Contains(r.stderr, "schema is incomplete") {
		t.Fatalf("verify before apply: %+v", r)
	}

	r = exec(t, srv, "", "schema", "apply")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "✓ created create_template host") || !strings.Contains(r.stdout, "schema reconciled: 7 actions, 0 re-reads") {
		t.Fatalf("apply: %+v", r)
	}
	if tpls := srv.Templates(); len(tpls) != 1 || len(tpls[0].AttributeSlugs) != 4 {
		t.Fatalf("server state: %+v", tpls)
	}

	r = exec(t, srv, "", "schema", "plan")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "no changes") {
		t.Fatalf("plan after apply: %+v", r)
	}
	r = exec(t, srv, "", "schema", "apply")
	if r.code != cli.ExitOK || !strings.Contains(r.stdout, "no changes") {
		t.Fatalf("apply after apply: %+v", r)
	}
	r = exec(t, srv, "", "schema", "verify")
	if r.code != cli.ExitOK || r.stdout != "schema complete\n" {
		t.Fatalf("verify after apply: %+v", r)
	}
}

// FR-020 JSON plan.
func TestSchema_PlanJSON(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	r := exec(t, srv, "", "schema", "plan", "--json")
	if r.code != cli.ExitChanges {
		t.Fatalf("%+v", r)
	}
	var doc struct {
		Version int              `json:"version"`
		Actions []map[string]any `json:"actions"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil || doc.Version != 1 || len(doc.Actions) != 7 {
		t.Fatalf("json: %v %+v\n%s", err, doc, r.stdout)
	}
}

// US-2: config overrides fit an existing schema; US-3: conflicts → exit 1, no writes; US-4: switches.
func TestSchema_OverridesConflictsSwitches(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("cpu_usage", "metric")
	srv.AddAttribute("ip_address", "string")
	srv.AddTemplate("server", "Server", "cpu_usage")
	cfg := `
schema:
  host_template: server
modules:
  probe:
    attributes:
      usage: { slug: cpu_usage }
  disk:
    enabled: true
`
	r := exec(t, srv, cfg, "schema", "plan")
	if r.code != cli.ExitChanges {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.stdout, "cpu_usage") || strings.Contains(r.stdout, "+ template server") {
		t.Errorf("existing objects must not be planned:\n%s", r.stdout)
	}
	for _, want := range []string{"+ template disk", "+ attribute disk_count (number) → server", "+ attribute disk_mount (text) → disk", "+ attribute probe_model (text) → server"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("plan lacks %q:\n%s", want, r.stdout)
		}
	}

	// conflict: probe_model exists as number
	srv.AddAttribute("probe_model", "number")
	before := len(srv.Requests())
	r = exec(t, srv, cfg, "schema", "apply")
	if r.code != cli.ExitError || !strings.Contains(r.stdout, "! conflict probe_model") || !strings.Contains(r.stderr, "nothing written") {
		t.Fatalf("conflict apply: %+v", r)
	}
	if got := len(srv.Requests()) - before; got != 1 {
		t.Fatalf("conflict must only read the schema, made %d requests", got)
	}

	// unknown module in config
	r = exec(t, srv, "modules:\n  gpu:\n    enabled: true\n", "schema", "plan")
	if r.code != cli.ExitError || !strings.Contains(r.stderr, `unknown module "gpu"`) {
		t.Fatalf("unknown module: %+v", r)
	}
	// required module disabled
	r = exec(t, srv, "modules:\n  ident:\n    enabled: false\n", "schema", "plan")
	if r.code != cli.ExitError || !strings.Contains(r.stderr, "required") {
		t.Fatalf("required off: %+v", r)
	}
	// unknown attribute key in override
	r = exec(t, srv, "modules:\n  probe:\n    attributes:\n      temp: { slug: t }\n", "schema", "plan")
	if r.code != cli.ExitError || !strings.Contains(r.stderr, `module "probe" has no attribute "temp"`) {
		t.Fatalf("unknown key: %+v", r)
	}
}

// US-5/2, FR-027: token without schema permission → clear guidance.
func TestSchema_Forbidden(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.DenyWrites = true
	r := exec(t, srv, "", "schema", "apply")
	if r.code != cli.ExitError || !strings.Contains(r.stderr, "cannot write the schema") || !strings.Contains(r.stderr, "schema.mode: verify") {
		t.Fatalf("%+v", r)
	}
	if len(srv.Templates()) != 0 {
		t.Fatal("nothing must be created")
	}
	// verify works with the same token once the schema exists
	srv.DenyWrites = false
	if r := exec(t, srv, "", "schema", "apply"); r.code != 0 {
		t.Fatalf("setup apply: %+v", r)
	}
	srv.DenyWrites = true
	if r := exec(t, srv, "", "schema", "verify"); r.code != 0 {
		t.Fatalf("verify with read-only token: %+v", r)
	}
}

func TestSchema_AuthErrors(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	env := map[string]string{"OMNISMITH_ACCESS_TOKEN": "bad", "OMNISMITH_PROJECT_ID": omnitest.ProjectID, "OMNISMITH_BASE_URL": srv.URL}
	app := &cli.App{Registry: moduletest.Registry(), Version: "t"}
	var out, errb bytes.Buffer
	code := app.Run(context.Background(), []string{"schema", "plan"}, &out, &errb, func(k string) string { return env[k] })
	if code != cli.ExitError || !strings.Contains(errb.String(), "unauthorized") || !strings.Contains(errb.String(), "OMNISMITH_ACCESS_TOKEN") {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	// wrong project id → guidance about the project id, not about permissions
	env["OMNISMITH_ACCESS_TOKEN"] = omnitest.Token
	srv.FailNext(omnitest.Fault{Status: 403, Body: `{"title":"Project Access Denied","status":403,"code":"project_access_denied"}`})
	out.Reset()
	errb.Reset()
	code = app.Run(context.Background(), []string{"schema", "plan"}, &out, &errb, func(k string) string { return env[k] })
	if code != cli.ExitError || !strings.Contains(errb.String(), "no access to this project") || !strings.Contains(errb.String(), "OMNISMITH_PROJECT_ID") || strings.Contains(errb.String(), "cannot write") {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
}

// FR-028: logs never contain the token or the project id, in text or JSON.
func TestLogging_NoSecrets(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		srv := omnitest.New()
		defer srv.Close()
		r := exec(t, srv, "", "--log-level", "debug", "--log-format", format, "schema", "apply")
		if r.code != 0 {
			t.Fatalf("%s: %+v", format, r)
		}
		if strings.Contains(r.stderr, omnitest.Token) || strings.Contains(r.stderr, omnitest.ProjectID) {
			t.Fatalf("%s: secrets leaked into logs:\n%s", format, r.stderr)
		}
		if strings.Contains(r.stdout, omnitest.Token) {
			t.Fatalf("%s: token in stdout", format)
		}
		if format == "json" && !strings.Contains(r.stderr, `"msg":"schema action applied"`) {
			t.Fatalf("json logs expected on first apply:\n%s", r.stderr)
		}
	}
}
