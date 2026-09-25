package cli_test

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/winsvc"
	"github.com/omnismith-apps/omnistat/internal/winsvc/fakehost"
)

const proxySecret = "http://svc:PROXY_SENTINEL@proxy.client.local:3128" //nolint:gosec // fake sentinel, asserted never printed

func serviceHost(t *testing.T) *fakehost.Host {
	return &fakehost.Host{
		IsElevated:      true,
		Exe:             `C:\Users\op\Downloads\omnistat.exe`,
		ProgDir:         `C:\Program Files\omnistat`,
		CfgDir:          t.TempDir(), // absent omnistat.yaml → defaults (FR-012)
		StateAfterStart: winsvc.StateRunning,
	}
}

func runService(t *testing.T, h *fakehost.Host, env map[string]string, args ...string) run {
	t.Helper()
	return runServiceFS(t, fstest.MapFS{"etc/machine-id": {Data: []byte(rawID)}}, h, env, args...)
}

func runServiceFS(t *testing.T, fsys fstest.MapFS, h *fakehost.Host, env map[string]string, args ...string) run {
	t.Helper()
	app := &cli.App{Registry: registryWithMachineID(fsys), Version: "t", ServiceHost: h, ServiceSettle: time.Millisecond}
	var out, errb bytes.Buffer
	code := app.Run(context.Background(), append([]string{"--log-level", "debug"}, args...), &out, &errb, func(k string) string { return env[k] })
	return run{code, out.String(), errb.String()}
}

func apiEnv(srv *omnitest.Server) map[string]string {
	return map[string]string{
		"OMNISMITH_ACCESS_TOKEN": omnitest.Token,
		"OMNISMITH_PROJECT_ID":   omnitest.ProjectID,
		"OMNISMITH_BASE_URL":     srv.URL,
		"HTTPS_PROXY":            proxySecret,
	}
}

// US-1/1, FR-006, FR-016: install against a fresh project (schema not applied
// yet, which the service does itself); stored settings named, never shown.
func TestServiceInstall(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	h := serviceHost(t)
	r := runService(t, h, apiEnv(srv), "service", "install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{
		"identity: " + machineid.Derive(rawID) + " (linux-machine-id)",
		"no omnistat schema yet",
		`binary:   C:\Program Files\omnistat\omnistat.exe`,
		"not present — defaults apply",
		"settings: HTTPS_PROXY, OMNISMITH_ACCESS_TOKEN, OMNISMITH_BASE_URL, OMNISMITH_PROJECT_ID",
		"done: start the service",
		"installed and running",
		"source \"omnistat\"",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout should contain %q:\n%s", want, r.stdout)
		}
	}
	assertNoSecrets(t, r)
	if h.Svc.Env["OMNISMITH_ACCESS_TOKEN"] != omnitest.Token || h.Svc.Env["HTTPS_PROXY"] != proxySecret {
		t.Fatal("settings must be stored for the service")
	}
	if len(srv.Entities()) != 0 || len(srv.Templates()) != 0 {
		t.Fatal("the pre-check is read-only (FR-006)")
	}
}

// US-1/6, FR-028: dry-run prints the steps, stores nothing, leaks nothing.
func TestServiceInstall_DryRun(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	h := serviceHost(t)
	r := runService(t, h, apiEnv(srv), "service", "install", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "dry run") || !strings.Contains(r.stdout, "would register service omnistat") || len(h.Calls()) != 0 {
		t.Fatalf("%+v calls=%v", r, h.Calls())
	}
	assertNoSecrets(t, r)
}

// US-1/5, FR-006: a failed pre-check gives exactly `identity`'s message and
// changes nothing.
func TestServiceInstall_PrecheckMatchesIdentity(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	for name, env := range map[string]map[string]string{
		"bad token":      {"OMNISMITH_ACCESS_TOKEN": "omni_wrong", "OMNISMITH_PROJECT_ID": omnitest.ProjectID, "OMNISMITH_BASE_URL": srv.URL},
		"no access":      {"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "OMNISMITH_PROJECT_ID": "01a0c47a-0000-7000-8000-00000000dead", "OMNISMITH_BASE_URL": srv.URL},
		"no project set": {"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "OMNISMITH_BASE_URL": srv.URL},
	} {
		t.Run(name, func(t *testing.T) {
			h := serviceHost(t)
			inst := runService(t, h, env, "service", "install")
			if inst.code != 1 || len(h.Calls()) != 0 {
				t.Fatalf("%+v calls=%v", inst, h.Calls())
			}
			if name == "no project set" {
				if !strings.Contains(inst.stderr, "OMNISMITH_PROJECT_ID") {
					t.Fatalf("the service cannot run without a project: %s", inst.stderr)
				}
				return
			}
			id := runService(t, nil, env, "identity")
			if lastLine(inst.stderr) != lastLine(id.stderr) {
				t.Fatalf("install and identity must say the same thing:\ninstall:  %s\nidentity: %s", lastLine(inst.stderr), lastLine(id.stderr))
			}
		})
	}
}

// US-1/5: no discoverable identity → identity's message, nothing changed.
func TestServiceInstall_NoIdentity(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	h := serviceHost(t)
	env := map[string]string{"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "OMNISMITH_PROJECT_ID": omnitest.ProjectID, "OMNISMITH_BASE_URL": srv.URL}
	inst := runServiceFS(t, fstest.MapFS{}, h, env, "service", "install")
	id := runServiceFS(t, fstest.MapFS{}, nil, env, "identity")
	if inst.code != 1 || len(h.Calls()) != 0 || !strings.Contains(inst.stderr, "no host identity") || lastLine(inst.stderr) != lastLine(id.stderr) {
		t.Fatalf("install: %+v\nidentity: %+v", inst, id)
	}
}

// FR-005: not elevated → the Administrator hint, nothing changed.
func TestServiceInstall_NotElevated(t *testing.T) {
	h := serviceHost(t)
	h.IsElevated = false
	r := runService(t, h, nil, "service", "install")
	if r.code != 1 || !strings.Contains(r.stderr, "Run as administrator") || len(h.Calls()) != 0 {
		t.Fatalf("%+v", r)
	}
}

// US-6: uninstall — not installed is fine; installed is removed; secrets never shown.
func TestServiceUninstall(t *testing.T) {
	h := serviceHost(t)
	r := runService(t, h, nil, "service", "uninstall")
	if r.code != 0 || !strings.Contains(r.stdout, "not installed") {
		t.Fatalf("%+v", r)
	}
	h.Svc = winsvc.Installed{Exists: true, Binary: `C:\Program Files\omnistat\omnistat.exe`, State: winsvc.StateRunning,
		Env: map[string]string{"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "HTTPS_PROXY": proxySecret}}
	r = runService(t, h, nil, "service", "uninstall", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "would remove service omnistat") || len(h.Calls()) != 0 {
		t.Fatalf("%+v %v", r, h.Calls())
	}
	r = runService(t, h, nil, "service", "uninstall")
	if r.code != 0 || !strings.Contains(r.stdout, "the host entity remains") || h.Svc.Exists {
		t.Fatalf("%+v", r)
	}
	assertNoSecrets(t, r)
}

// FR-027: off Windows the command says it is unsupported.
func TestService_UnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows the real service manager would be used")
	}
	app := &cli.App{Registry: registryWithMachineID(fstest.MapFS{}), Version: "t"}
	var out, errb bytes.Buffer
	code := app.Run(context.Background(), []string{"service", "install"}, &out, &errb, func(string) string { return "" })
	if code != 1 || !strings.Contains(errb.String(), "not supported on "+runtime.GOOS) {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
}

func assertNoSecrets(t *testing.T, r run) {
	t.Helper()
	for _, s := range []string{omnitest.Token, "PROXY_SENTINEL"} {
		if strings.Contains(r.stdout+r.stderr, s) {
			t.Fatalf("secret %q leaked (FR-016):\n%s\n%s", s, r.stdout, r.stderr)
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
