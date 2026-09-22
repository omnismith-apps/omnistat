package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/module/moduletest"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

const rawID = "0123456789abcdef0123456789abcdef"

// registryWithMachineID mirrors the production registry with a fake OS.
func registryWithMachineID(fsys fstest.MapFS) *module.Registry {
	m := machineid.New()
	m.GOOS = "linux"
	m.FS = fsys
	r := module.NewRegistry()
	r.Register(m, module.Required())
	r.Register(moduletest.Probe())
	return r
}

func runIdentity(t *testing.T, srv *omnitest.Server, fsys fstest.MapFS, extraEnv map[string]string, args ...string) run {
	t.Helper()
	env := map[string]string{}
	if srv != nil {
		env["OMNISMITH_ACCESS_TOKEN"] = omnitest.Token
		env["OMNISMITH_PROJECT_ID"] = omnitest.ProjectID
		env["OMNISMITH_BASE_URL"] = srv.URL
	}
	for k, v := range extraEnv {
		env[k] = v
	}
	app := &cli.App{Registry: registryWithMachineID(fsys), Version: "t"}
	var out, errb bytes.Buffer
	code := app.Run(context.Background(), append([]string{"--log-level", "debug"}, args...), &out, &errb, func(k string) string { return env[k] })
	return run{code, out.String(), errb.String()}
}

// FR-017 without credentials: identity and source only, nothing else needed.
func TestIdentity_NoProject(t *testing.T) {
	fsys := fstest.MapFS{"etc/machine-id": {Data: []byte(rawID + "\n")}}
	r := runIdentity(t, nil, fsys, nil, "identity")
	if r.code != 0 || !strings.Contains(r.stdout, "identity: "+machineid.Derive(rawID)) || !strings.Contains(r.stdout, "source:   linux-machine-id") || !strings.Contains(r.stdout, "not checked") {
		t.Fatalf("%+v", r)
	}
	// FR-018: the raw id never appears anywhere
	if strings.Contains(r.stdout+r.stderr, rawID) {
		t.Fatalf("raw machine id leaked:\n%s%s", r.stdout, r.stderr)
	}
}

// FR-006: no identity → fail with guidance; FR-009: env static wins.
func TestIdentity_AbsentAndStatic(t *testing.T) {
	r := runIdentity(t, nil, fstest.MapFS{}, nil, "identity")
	if r.code != 1 || !strings.Contains(r.stderr, "no host identity") || !strings.Contains(r.stderr, "OMNISTAT_IDENTITY") {
		t.Fatalf("%+v", r)
	}
	r = runIdentity(t, nil, fstest.MapFS{}, map[string]string{"OMNISTAT_IDENTITY": "rack7-node3"}, "identity", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil || rep["identity"] != "rack7-node3" || rep["source"] != "static" || rep["project_checked"] != false {
		t.Fatalf("json: %v %+v", err, rep)
	}
}

// US-5/1 end to end: schema not ready → guidance; after apply → would create;
// after an entity exists → resolves to it; duplicates warned. Never writes.
func TestIdentity_Resolution(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	fsys := fstest.MapFS{"etc/machine-id": {Data: []byte(rawID)}}
	want := machineid.Derive(rawID)

	r := runIdentity(t, srv, fsys, nil, "identity")
	if r.code != 1 || !strings.Contains(r.stderr, "schema is not ready") || !strings.Contains(r.stdout, "identity: "+want) {
		t.Fatalf("before schema: %+v", r)
	}
	if r := exec2(t, srv, registryWithMachineID(fsys), "schema", "apply"); r.code != 0 {
		t.Fatalf("apply: %+v", r)
	}
	r = runIdentity(t, srv, fsys, nil, "identity", "--json")
	var rep map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil || r.code != 0 || rep["would_create"] != true || rep["schema_ok"] != true || rep["entity_id"] != nil {
		t.Fatalf("would create: %v %+v %+v", err, rep, r)
	}
	if len(srv.Entities()) != 0 {
		t.Fatal("identity must not write")
	}

	id := srv.AddEntity("host", map[string]any{"machine_id": want}, "2026-09-20T00:00:00Z")
	r = runIdentity(t, srv, fsys, nil, "identity")
	if r.code != 0 || !strings.Contains(r.stdout, "entity:   "+id) {
		t.Fatalf("resolved: %+v", r)
	}
	srv.AddEntity("host", map[string]any{"machine_id": want}, "2026-09-21T00:00:00Z")
	r = runIdentity(t, srv, fsys, nil, "identity")
	if r.code != 0 || !strings.Contains(r.stdout, "entity:   "+id) || !strings.Contains(r.stdout, "2 entities carry this identity") {
		t.Fatalf("duplicates: %+v", r)
	}
	if strings.Contains(r.stdout+r.stderr, rawID) {
		t.Fatal("raw machine id leaked")
	}
}

// FR-002: the identity module cannot be disabled.
func TestIdentity_Required(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	app := &cli.App{Registry: registryWithMachineID(fstest.MapFS{}), Version: "t"}
	cfg := t.TempDir() + "/c.yaml"
	if err := writeFile(cfg, "modules:\n  machine-id:\n    enabled: false\n"); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	env := map[string]string{"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "OMNISMITH_PROJECT_ID": omnitest.ProjectID, "OMNISMITH_BASE_URL": srv.URL}
	code := app.Run(context.Background(), []string{"--config", cfg, "schema", "plan"}, &out, &errb, func(k string) string { return env[k] })
	if code != 1 || !strings.Contains(errb.String(), `module "machine-id" is required`) {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
}
