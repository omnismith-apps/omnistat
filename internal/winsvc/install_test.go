package winsvc_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/service"
	"github.com/omnismith-apps/omnistat/internal/winsvc"
	"github.com/omnismith-apps/omnistat/internal/winsvc/fakehost"
)

const (
	progDir   = `C:\Program Files\omnistat`
	cfgDir    = `C:\ProgramData\omnistat`
	installed = progDir + `\omnistat.exe`
	download  = `C:\Users\op\Downloads\omnistat_0.2.0_windows_amd64\omnistat.exe`
	secret    = "omni_live_SENTINEL_never_printed" //nolint:gosec // fake sentinel, asserted never printed
)

func newHost() *fakehost.Host {
	return &fakehost.Host{
		IsElevated:      true,
		Exe:             download,
		ProgDir:         progDir,
		CfgDir:          cfgDir,
		StateAfterStart: winsvc.StateRunning,
	}
}

func getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// precheck records what the install pre-check was given (FR-006).
type precheck struct {
	calls   int
	env     map[string]string
	cfgPath string
	err     error
}

func (p *precheck) fn(_ context.Context, getenv func(string) string, cfgPath string) (service.Checked, error) {
	p.calls++
	p.cfgPath = cfgPath
	p.env = map[string]string{}
	for _, k := range service.Captured {
		if v := getenv(k); v != "" {
			p.env[k] = v
		}
	}
	return service.Checked{Identity: "f20a98…", Source: "windows-machine-guid"}, p.err
}

func opts(pc *precheck, env map[string]string) service.InstallOptions {
	return service.InstallOptions{Getenv: getenv(env), Precheck: pc.fn}
}

func descs(p *service.Plan) string {
	var b bytes.Buffer
	p.Describe(&b)
	return b.String()
}

// FR-005: not elevated → nothing happens at all, not even the pre-check or a prompt.
func TestInstall_NotElevated(t *testing.T) {
	h := newHost()
	h.IsElevated = false
	pc := &precheck{}
	_, err := winsvc.PlanInstall(context.Background(), h, opts(pc, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}))
	if !errors.Is(err, winsvc.ErrNotElevated) || len(h.Calls()) != 0 || pc.calls != 0 || h.Prompts() != 0 {
		t.Fatalf("err %v, calls %v, prechecks %d, prompts %d", err, h.Calls(), pc.calls, h.Prompts())
	}
}

// US-1/1, FR-006–FR-011, FR-013: a fresh install, in order.
func TestInstall_Fresh(t *testing.T) {
	h := newHost()
	pc := &precheck{}
	env := map[string]string{"OMNISMITH_ACCESS_TOKEN": secret, "OMNISMITH_PROJECT_ID": "p1", "PATH": `C:\Windows`}
	p, err := winsvc.PlanInstall(context.Background(), h, opts(pc, env))
	if err != nil {
		t.Fatal(err)
	}
	if pc.calls != 1 || pc.cfgPath != cfgDir+`\omnistat.yaml` || pc.env["OMNISMITH_ACCESS_TOKEN"] != secret || pc.env["OMNISMITH_PROJECT_ID"] != "p1" {
		t.Fatalf("pre-check must see the settings the service will get: %+v", pc)
	}
	if len(h.Calls()) != 0 {
		t.Fatalf("planning must not change anything: %v", h.Calls())
	}
	var out bytes.Buffer
	if err := p.Apply(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CopyBinary " + download + " -> " + installed,
		"EnsureConfigDir " + cfgDir,
		"CreateFile " + cfgDir + `\omnistat.yaml`,
		"Register " + installed,
		"StoreEnv 2",
		"EventSource true",
		"Start",
	}
	if got := h.Calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	r := h.Registered()
	if r.Account != `NT SERVICE\omnistat` || strings.Join(r.Args, " ") != "run --daemon" || r.RestartDelay != time.Minute || r.DisplayName == "" || r.Description == "" {
		t.Fatalf("registration: %+v", r)
	}
	if h.Svc.Env["OMNISMITH_ACCESS_TOKEN"] != secret || h.Svc.Env["OMNISMITH_PROJECT_ID"] != "p1" || h.Svc.Env["PATH"] != "" {
		t.Fatalf("only the captured settings are stored: %v", keys(h.Svc.Env))
	}
	if !h.EventSourceRegistered() || h.Prompts() != 0 {
		t.Fatal("event source registered, no prompt with a token in the environment")
	}
}

// FR-014: no token in the environment → prompt; no console → fail before anything.
func TestInstall_TokenPrompt(t *testing.T) {
	h := newHost()
	h.Secret = "  " + secret + "  "
	pc := &precheck{}
	p, err := winsvc.PlanInstall(context.Background(), h, opts(pc, map[string]string{"OMNISMITH_PROJECT_ID": "p1"}))
	if err != nil || h.Prompts() != 1 || pc.env["OMNISMITH_ACCESS_TOKEN"] != secret {
		t.Fatalf("prompted token (trimmed) goes to the pre-check: %v %d %v", err, h.Prompts(), pc.env)
	}
	if err := p.Apply(context.Background(), &bytes.Buffer{}); err != nil || h.Svc.Env["OMNISMITH_ACCESS_TOKEN"] != secret {
		t.Fatalf("stored: %v", err)
	}

	for name, h := range map[string]*fakehost.Host{
		"no console":   func() *fakehost.Host { h := newHost(); h.SecretErr = service.ErrNotInteractive; return h }(),
		"empty answer": func() *fakehost.Host { h := newHost(); h.Secret = "   "; return h }(),
	} {
		t.Run(name, func(t *testing.T) {
			pc := &precheck{}
			_, err := winsvc.PlanInstall(context.Background(), h, opts(pc, nil))
			if !errors.Is(err, service.ErrNoToken) || pc.calls != 0 || len(h.Calls()) != 0 {
				t.Fatalf("err %v, prechecks %d, calls %v", err, pc.calls, h.Calls())
			}
		})
	}
}

// FR-006 / US-1/5: a failed pre-check is returned as is and changes nothing.
func TestInstall_PrecheckFails(t *testing.T) {
	h := newHost()
	denied := errors.New("the token has no access to project p1 (project_access_denied)")
	pc := &precheck{err: denied}
	_, err := winsvc.PlanInstall(context.Background(), h, opts(pc, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}))
	if !errors.Is(err, denied) || err.Error() != denied.Error() || len(h.Calls()) != 0 {
		t.Fatalf("the identity message, unchanged, and no calls: %v %v", err, h.Calls())
	}
}

// FR-028 / US-1/6: the dry-run lists every step, names settings without values,
// and applies nothing.
func TestInstall_DryRun(t *testing.T) {
	h := newHost()
	pc := &precheck{}
	p, err := winsvc.PlanInstall(context.Background(), h, opts(pc, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret, "HTTPS_PROXY": "http://user:pw@proxy:3128"})) //nolint:gosec // fake credentials, asserted never printed
	if err != nil {
		t.Fatal(err)
	}
	d := descs(p)
	for _, want := range []string{installed, cfgDir, `NT SERVICE\omnistat`, "run --daemon", "delayed", "1m0s", "OMNISMITH_ACCESS_TOKEN", "HTTPS_PROXY", "event source omnistat", "start"} {
		if !strings.Contains(d, want) {
			t.Errorf("dry-run should mention %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, secret) || strings.Contains(d, "pw@") {
		t.Fatalf("dry-run leaked a value:\n%s", d)
	}
	if len(h.Calls()) != 0 {
		t.Fatalf("dry-run changed something: %v", h.Calls())
	}
}

// Edge case: install run from the installed binary has nothing to copy.
func TestInstall_FromInstalledBinary(t *testing.T) {
	h := newHost()
	h.Exe = strings.ToUpper(installed) // paths compare case-insensitively
	h.Svc = winsvc.Installed{Exists: true, Binary: installed, State: winsvc.StateStopped, Env: map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}}
	p, err := winsvc.PlanInstall(context.Background(), h, opts(&precheck{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.Calls() {
		if strings.HasPrefix(c, "CopyBinary") || c == "Stop" {
			t.Fatalf("nothing to copy or stop: %v", h.Calls())
		}
	}
}

// US-5/1, US-5/3, FR-017: upgrade stops first, replaces the binary, keeps
// stored settings, replaces those set now, and asks for no token.
func TestInstall_Upgrade(t *testing.T) {
	h := newHost()
	h.Svc = winsvc.Installed{Exists: true, Binary: `"` + installed + `"`, State: winsvc.StateRunning, Env: map[string]string{
		"OMNISMITH_ACCESS_TOKEN": secret, "OMNISMITH_PROJECT_ID": "p1", "HTTPS_PROXY": "http://old:3128",
	}}
	pc := &precheck{}
	p, err := winsvc.PlanInstall(context.Background(), h, opts(pc, map[string]string{"HTTPS_PROXY": "http://new:3128"}))
	if err != nil {
		t.Fatal(err)
	}
	if h.Prompts() != 0 || pc.env["OMNISMITH_ACCESS_TOKEN"] != secret {
		t.Fatalf("stored token is kept and pre-checked, no prompt: %d %v", h.Prompts(), pc.env)
	}
	if err := p.Apply(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	calls := h.Calls()
	if calls[0] != "Stop" || !strings.HasPrefix(calls[1], "CopyBinary") || calls[len(calls)-1] != "Start" {
		t.Fatalf("stop, replace, …, start: %v", calls)
	}
	if e := h.Svc.Env; e["OMNISMITH_ACCESS_TOKEN"] != secret || e["OMNISMITH_PROJECT_ID"] != "p1" || e["HTTPS_PROXY"] != "http://new:3128" {
		t.Fatalf("merge: %v", keys(e))
	}
}

// FR-018 / US-5/2: --replace-token prompts even with a stored token.
func TestInstall_ReplaceToken(t *testing.T) {
	h := newHost()
	h.Svc = winsvc.Installed{Exists: true, Binary: installed, State: winsvc.StateRunning, Env: map[string]string{"OMNISMITH_ACCESS_TOKEN": "omni_old"}}
	h.Secret = secret
	o := opts(&precheck{}, nil)
	o.ReplaceToken = true
	p, err := winsvc.PlanInstall(context.Background(), h, o)
	if err != nil || h.Prompts() != 1 {
		t.Fatalf("%v prompts=%d", err, h.Prompts())
	}
	if err := p.Apply(context.Background(), &bytes.Buffer{}); err != nil || h.Svc.Env["OMNISMITH_ACCESS_TOKEN"] != secret {
		t.Fatalf("replaced: %v", err)
	}
}

// FR-019: someone else's "omnistat" service is left alone.
func TestInstall_ForeignService(t *testing.T) {
	h := newHost()
	h.Svc = winsvc.Installed{Exists: true, Binary: `D:\vendor\agent\omnistat.exe`}
	pc := &precheck{}
	_, err := winsvc.PlanInstall(context.Background(), h, opts(pc, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}))
	if !errors.Is(err, winsvc.ErrForeignService) || !strings.Contains(err.Error(), `D:\vendor\agent\omnistat.exe`) || len(h.Calls()) != 0 || pc.calls != 0 {
		t.Fatalf("%v %v", err, h.Calls())
	}
}

// FR-011: a service that stops right after starting fails the install.
func TestInstall_ServiceDiesAfterStart(t *testing.T) {
	h := newHost()
	h.StateAfterStart = winsvc.StateStopped
	p, err := winsvc.PlanInstall(context.Background(), h, opts(&precheck{}, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(context.Background(), &bytes.Buffer{}); !errors.Is(err, winsvc.ErrStartFailed) {
		t.Fatalf("want ErrStartFailed, got %v", err)
	}
}

// Edge case: loose permissions on an existing config directory are tightened and said.
func TestInstall_ReportsTightenedConfigDir(t *testing.T) {
	h := newHost()
	h.LooseConfigDir = true
	p, err := winsvc.PlanInstall(context.Background(), h, opts(&precheck{}, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Apply(context.Background(), &out); err != nil || !strings.Contains(out.String(), "tightened") {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), secret) {
		t.Fatal("apply output leaked the token")
	}
}

// A failing step stops the apply and names the step.
func TestInstall_StepFailureStops(t *testing.T) {
	h := newHost()
	h.Fail = map[string]error{"Register": errors.New("access denied")}
	p, err := winsvc.PlanInstall(context.Background(), h, opts(&precheck{}, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}))
	if err != nil {
		t.Fatal(err)
	}
	err = p.Apply(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "access denied") || !strings.Contains(err.Error(), "register") {
		t.Fatalf("%v", err)
	}
	for _, c := range h.Calls() {
		if c == "Start" || strings.HasPrefix(c, "StoreEnv") {
			t.Fatalf("must stop at the failing step: %v", h.Calls())
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// 007 FR-017 (amends 006): a project id set nowhere is asked for and stored;
// one in the environment needs no prompt.
func TestInstall_ProjectPrompt(t *testing.T) {
	h := newHost()
	h.CfgDir = t.TempDir() // no config file
	h.Line = "p-typed"
	pc := &precheck{}
	p, err := winsvc.PlanInstall(context.Background(), h, opts(pc, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}))
	if err != nil || h.LinePrompts() != 1 || pc.env["OMNISMITH_PROJECT_ID"] != "p-typed" {
		t.Fatalf("%v prompts=%d %v", err, h.LinePrompts(), pc.env)
	}
	if err := p.Apply(context.Background(), &bytes.Buffer{}); err != nil || h.Svc.Env["OMNISMITH_PROJECT_ID"] != "p-typed" {
		t.Fatalf("stored: %v", err)
	}

	h = newHost()
	if _, err := winsvc.PlanInstall(context.Background(), h, opts(&precheck{}, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret, "OMNISMITH_PROJECT_ID": "p1"})); err != nil || h.LinePrompts() != 0 {
		t.Fatalf("%v prompts=%d", err, h.LinePrompts())
	}
}

// 007 FR-014 (amends 006 FR-012): install writes the commented starter when no
// config file exists, says so in the dry-run, and never touches an existing one.
func TestInstall_StarterConfig(t *testing.T) {
	h := newHost()
	p, err := winsvc.PlanInstall(context.Background(), h, opts(&precheck{}, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}))
	if err != nil {
		t.Fatal(err)
	}
	if d := descs(p); !strings.Contains(d, "would create "+cfgDir+`\omnistat.yaml: a commented starter`) {
		t.Fatalf("dry-run:\n%s", d)
	}
	if err := p.Apply(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := h.Created[cfgDir+`\omnistat.yaml`]; !bytes.Equal(got, config.Starter) {
		t.Fatalf("starter written: %d bytes", len(got))
	}

	// A config file that exists is left alone: no step at all.
	h = newHost()
	h.Existing = map[string]bool{cfgDir + `\omnistat.yaml`: true}
	h.Svc = winsvc.Installed{Exists: true, Binary: installed, Env: map[string]string{"OMNISMITH_ACCESS_TOKEN": secret, "OMNISMITH_PROJECT_ID": "p1"}}
	p, err = winsvc.PlanInstall(context.Background(), h, opts(&precheck{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.Calls() {
		if strings.HasPrefix(c, "CreateFile") {
			t.Fatalf("an existing config must not be touched: %v", h.Calls())
		}
	}
}
