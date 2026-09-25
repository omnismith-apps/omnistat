package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/systemd"
	"github.com/omnismith-apps/omnistat/internal/systemd/fakehost"
)

// The systemd backend through the CLI (spec 007), on the fake host: nothing
// here touches the real systemd.

const linuxDownload = "/home/op/Downloads/omnistat"

func runSystemd(t *testing.T, h *fakehost.Host, env map[string]string, args ...string) run {
	t.Helper()
	app := &cli.App{Registry: registryWithMachineID(fstest.MapFS{"etc/machine-id": {Data: []byte(rawID)}}), Version: "t", SystemdHost: h, ServiceSettle: time.Millisecond}
	var out, errb bytes.Buffer
	code := app.Run(context.Background(), append([]string{"--log-level", "debug"}, args...), &out, &errb, func(k string) string { return env[k] })
	return run{code, out.String(), errb.String()}
}

// US-1/1, FR-005, FR-016: install against a fresh project; the report names
// the unit, the binary, the config and the stored settings, never a value.
func TestSystemdInstall(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	h := fakehost.New(linuxDownload)
	env := apiEnv(srv)
	env["https_proxy"] = proxySecret
	r := runSystemd(t, h, env, "service", "install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{
		"identity: " + machineid.Derive(rawID) + " (linux-machine-id)",
		"unit:     /etc/systemd/system/omnistat.service",
		"binary:   /usr/local/bin/omnistat",
		"config:   /etc/omnistat/omnistat.yaml (not present — install creates a commented starter",
		"settings: HTTPS_PROXY, OMNISMITH_ACCESS_TOKEN, OMNISMITH_BASE_URL, OMNISMITH_PROJECT_ID, https_proxy",
		"done: start the service",
		"installed and running",
		"Logs: journalctl -u omnistat",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout should contain %q:\n%s", want, r.stdout)
		}
	}
	assertNoSecrets(t, r)
	e, _ := h.File(systemd.EnvPath)
	if stored, err := systemd.ParseEnv(e.Data); err != nil || stored["OMNISMITH_ACCESS_TOKEN"] != omnitest.Token || stored["https_proxy"] != proxySecret {
		t.Fatalf("settings stored for the service: %v", err)
	}
	if len(srv.Entities()) != 0 || len(srv.Templates()) != 0 {
		t.Fatal("the pre-check is read-only")
	}
}

// US-1/6, FR-024: the dry-run shows the unit and changes nothing.
func TestSystemdInstall_DryRun(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	h := fakehost.New(linuxDownload)
	r := runSystemd(t, h, apiEnv(srv), "service", "install", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "dry run") || !strings.Contains(r.stdout, "      DynamicUser=yes") || len(h.Calls()) != 0 {
		t.Fatalf("%+v calls=%v", r, h.Calls())
	}
	assertNoSecrets(t, r)
}

// US-1/5, FR-005: a failed pre-check says what `identity` says; nothing changes.
func TestSystemdInstall_PrecheckMatchesIdentity(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	env := map[string]string{"OMNISMITH_ACCESS_TOKEN": "omni_wrong", "OMNISMITH_PROJECT_ID": omnitest.ProjectID, "OMNISMITH_BASE_URL": srv.URL}
	h := fakehost.New(linuxDownload)
	inst := runSystemd(t, h, env, "service", "install")
	id := runService(t, nil, env, "identity")
	if inst.code != 1 || len(h.Calls()) != 0 || lastLine(inst.stderr) != lastLine(id.stderr) {
		t.Fatalf("install: %+v\nidentity: %+v", inst, id)
	}
}

// US-1/1, FR-015, FR-017: under sudo the environment is gone; the project id
// and token are asked for, and install goes on.
func TestSystemdInstall_Prompts(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	h := fakehost.New(linuxDownload)
	h.Line, h.Secret = omnitest.ProjectID, omnitest.Token
	r := runSystemd(t, h, map[string]string{"OMNISMITH_BASE_URL": srv.URL}, "service", "install")
	if s, l := h.Prompts(); r.code != 0 || s != 1 || l != 1 {
		t.Fatalf("%+v prompts %d/%d", r, s, l)
	}
	assertNoSecrets(t, r)
}

// US-1/4, FR-002: not root → the sudo hint, nothing changed.
func TestSystemdInstall_NotRoot(t *testing.T) {
	h := fakehost.New(linuxDownload)
	h.IsRoot = false
	r := runSystemd(t, h, nil, "service", "install")
	if r.code != 1 || !strings.Contains(r.stderr, "run it with sudo") || len(h.Calls()) != 0 {
		t.Fatalf("%+v", r)
	}
}

// US-6: uninstall — not installed is fine; installed is removed, the config kept.
func TestSystemdUninstall(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	h := fakehost.New(linuxDownload)
	r := runSystemd(t, h, nil, "service", "uninstall")
	if r.code != 0 || !strings.Contains(r.stdout, "not installed") {
		t.Fatalf("%+v", r)
	}
	if r := runSystemd(t, h, apiEnv(srv), "service", "install"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	r = runSystemd(t, h, nil, "service", "uninstall", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "would remove "+systemd.EnvPath) {
		t.Fatalf("%+v", r)
	}
	r = runSystemd(t, h, nil, "service", "uninstall")
	if r.code != 0 || !strings.Contains(r.stdout, "the host entity remains") || !strings.Contains(r.stdout, "kept /etc/omnistat") {
		t.Fatalf("%+v", r)
	}
	if _, ok := h.File(systemd.EnvPath); ok {
		t.Fatal("the token file must be gone")
	}
	assertNoSecrets(t, r)
}
