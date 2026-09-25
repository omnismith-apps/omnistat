package systemd_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/service"
	"github.com/omnismith-apps/omnistat/internal/systemd"
	"github.com/omnismith-apps/omnistat/internal/systemd/fakehost"
)

const (
	download = "/home/op/Downloads/omnistat_0.2.0_linux_amd64/omnistat"
	secret   = "omni_live_SENTINEL_never_printed" //nolint:gosec // fake sentinel, asserted never printed
	proxy    = "http://svc:PROXY_PW@proxy:3128"   //nolint:gosec // fake sentinel, asserted never printed
)

func getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// precheck records what the install pre-check was given (FR-005).
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
	for _, k := range append([]string{"https_proxy"}, service.Captured...) {
		if v := getenv(k); v != "" {
			p.env[k] = v
		}
	}
	return service.Checked{Identity: "f20a98…", Source: "linux-machine-id"}, p.err
}

func opts(pc *precheck, env map[string]string) service.InstallOptions {
	return service.InstallOptions{Getenv: getenv(env), Precheck: pc.fn}
}

func apiEnv() map[string]string {
	return map[string]string{"OMNISMITH_ACCESS_TOKEN": secret, "OMNISMITH_PROJECT_ID": "p1"}
}

func describe(p *service.Plan) string {
	var b bytes.Buffer
	p.Describe(&b)
	return b.String()
}

func apply(t *testing.T, p *service.Plan) string {
	t.Helper()
	var b bytes.Buffer
	if err := p.Apply(context.Background(), &b); err != nil {
		t.Fatalf("apply: %v\n%s", err, b.String())
	}
	return b.String()
}

// installed makes h look like a host where omnistat's service is installed
// and running, with stored settings and an edited config.
func installed(h *fakehost.Host, stored map[string]string) {
	data, err := systemd.FormatEnv(stored)
	if err != nil {
		panic(err)
	}
	h.Files[systemd.BinaryPath] = fakehost.Entry{Data: []byte("old omnistat"), Mode: 0o755}
	h.Files[systemd.ConfigDir] = fakehost.Entry{Dir: true, Mode: 0o755}
	h.Files[systemd.ConfigPath] = fakehost.Entry{Data: []byte("log: {level: debug}\n"), Mode: 0o644}
	h.Files[systemd.EnvPath] = fakehost.Entry{Data: data, Mode: 0o600}
	h.Files[systemd.UnitPath] = fakehost.Entry{Data: []byte(systemd.UnitText()), Mode: 0o644}
	h.State = systemd.Unit{LoadState: "loaded", FragmentPath: systemd.UnitPath, ActiveState: "active", SubState: "running"}
}

func noSecrets(t *testing.T, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if strings.Contains(s, secret) || strings.Contains(s, "PROXY_PW") {
			t.Fatalf("a setting's value leaked (FR-016):\n%s", s)
		}
	}
}

// FR-002, FR-004: not root, or no systemd → nothing happens at all, not even
// the pre-check or a prompt.
func TestInstall_Preconditions(t *testing.T) {
	for name, tc := range map[string]struct {
		mod  func(*fakehost.Host)
		want error
	}{
		"not root":   {func(h *fakehost.Host) { h.IsRoot = false }, systemd.ErrNotRoot},
		"no systemd": {func(h *fakehost.Host) { h.IsBooted = false }, systemd.ErrNotBooted},
	} {
		t.Run(name, func(t *testing.T) {
			h := fakehost.New(download)
			tc.mod(h)
			pc := &precheck{}
			_, err := systemd.PlanInstall(context.Background(), h, opts(pc, nil))
			s, l := h.Prompts()
			if !errors.Is(err, tc.want) || len(h.Calls()) != 0 || pc.calls != 0 || s+l != 0 {
				t.Fatalf("err %v, calls %v, prechecks %d, prompts %d", err, h.Calls(), pc.calls, s+l)
			}
		})
	}
}

// US-1/1, FR-005–FR-014: a fresh install, in order, with the files it writes.
func TestInstall_Fresh(t *testing.T) {
	h := fakehost.New(download)
	pc := &precheck{}
	env := apiEnv()
	env["https_proxy"], env["PATH"] = proxy, "/usr/bin"
	p, err := systemd.PlanInstall(context.Background(), h, opts(pc, env))
	if err != nil {
		t.Fatal(err)
	}
	if pc.calls != 1 || pc.cfgPath != systemd.ConfigPath || pc.env["OMNISMITH_ACCESS_TOKEN"] != secret || pc.env["https_proxy"] != proxy {
		t.Fatalf("the pre-check sees the settings and config the service will get: %+v", pc)
	}
	if len(h.Calls()) != 0 || p.Upgrade || p.Unit != systemd.UnitPath || p.Binary != systemd.BinaryPath || p.ConfigPath != systemd.ConfigPath || !strings.Contains(p.LogHint, "journalctl -u omnistat") {
		t.Fatalf("plan: %+v, calls %v", p, h.Calls())
	}
	if strings.Join(p.Stored, ",") != "OMNISMITH_ACCESS_TOKEN,OMNISMITH_PROJECT_ID,https_proxy" {
		t.Fatalf("stored names: %v", p.Stored)
	}
	out := apply(t, p)
	want := []string{
		"MkdirAll /usr/local/bin 0755",
		"WriteFile /usr/local/bin/omnistat 0755",
		"MkdirAll /etc/omnistat 0755",
		"CreateFile /etc/omnistat/omnistat.yaml 0644",
		"WriteFile /etc/omnistat/omnistat.env 0600",
		"WriteFile /etc/systemd/system/omnistat.service 0644",
		"systemctl daemon-reload",
		"systemctl enable omnistat.service",
		"systemctl start omnistat.service",
	}
	if got := h.Calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if b, _ := h.File(systemd.BinaryPath); string(b.Data) != "omnistat binary" {
		t.Fatal("the running binary is installed")
	}
	if c, _ := h.File(systemd.ConfigPath); !bytes.Equal(c.Data, config.Starter) {
		t.Fatal("the starter config is written")
	}
	if u, _ := h.File(systemd.UnitPath); string(u.Data) != systemd.UnitText() {
		t.Fatal("the unit is written")
	}
	e, _ := h.File(systemd.EnvPath)
	stored, err := systemd.ParseEnv(e.Data)
	if err != nil || len(stored) != 3 || stored["OMNISMITH_ACCESS_TOKEN"] != secret || stored["https_proxy"] != proxy || stored["PATH"] != "" {
		t.Fatalf("stored settings: %v %v", err, len(stored))
	}
	if s, l := h.Prompts(); s+l != 0 {
		t.Fatal("nothing to ask")
	}
	noSecrets(t, out, describe(p))
}

// US-1/6, FR-024, FR-016: the dry-run shows every change — the full unit
// included — names the settings, and changes nothing.
func TestInstall_DryRun(t *testing.T) {
	h := fakehost.New(download)
	env := apiEnv()
	env["HTTPS_PROXY"] = proxy
	p, err := systemd.PlanInstall(context.Background(), h, opts(&precheck{}, env))
	if err != nil {
		t.Fatal(err)
	}
	d := describe(p)
	for _, want := range []string{
		"install " + download + " as /usr/local/bin/omnistat (root, 0755)",
		"create /etc/omnistat (root, 0755)",
		"create /etc/omnistat/omnistat.yaml",
		"/etc/omnistat/omnistat.env (root, 0600",
		"HTTPS_PROXY, OMNISMITH_ACCESS_TOKEN, OMNISMITH_PROJECT_ID",
		"write /etc/systemd/system/omnistat.service (root, 0644)",
		"      DynamicUser=yes",
		"      " + systemd.Marker,
		"systemctl daemon-reload",
		"systemctl enable omnistat.service",
		"start the service",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("dry-run should show %q:\n%s", want, d)
		}
	}
	noSecrets(t, d)
	if len(h.Calls()) != 0 {
		t.Fatalf("dry-run changed something: %v", h.Calls())
	}
}

// FR-015, FR-017: what the environment lacks is asked for; without a terminal
// the token is an error before anything changes.
func TestInstall_Prompts(t *testing.T) {
	h := fakehost.New(download)
	h.Secret, h.Line = secret, "p-typed"
	pc := &precheck{}
	p, err := systemd.PlanInstall(context.Background(), h, opts(pc, nil))
	if s, l := h.Prompts(); err != nil || s != 1 || l != 1 || pc.env["OMNISMITH_PROJECT_ID"] != "p-typed" || pc.env["OMNISMITH_ACCESS_TOKEN"] != secret {
		t.Fatalf("%v prompts %d/%d %v", err, s, l, pc.env)
	}
	apply(t, p)

	h = fakehost.New(download)
	h.SecretErr, h.LineErr = service.ErrNotInteractive, service.ErrNotInteractive
	pc = &precheck{}
	if _, err := systemd.PlanInstall(context.Background(), h, opts(pc, nil)); !errors.Is(err, service.ErrNoToken) || pc.calls != 0 || len(h.Calls()) != 0 {
		t.Fatalf("%v %v", err, h.Calls())
	}
}

// FR-005 / US-1/5: a failed pre-check is returned as is and changes nothing.
func TestInstall_PrecheckFails(t *testing.T) {
	h := fakehost.New(download)
	denied := errors.New("the token has no access to project p1 (project_access_denied)")
	_, err := systemd.PlanInstall(context.Background(), h, opts(&precheck{err: denied}, apiEnv()))
	if !errors.Is(err, denied) || err.Error() != denied.Error() || len(h.Calls()) != 0 {
		t.Fatalf("%v %v", err, h.Calls())
	}
}

// US-5/1, US-5/3, US-5/4, FR-018: an upgrade stops first, replaces the binary
// and the unit, keeps the stored settings (replacing those set now), keeps
// the config and the drop-ins, asks nothing, and ends running.
func TestInstall_Upgrade(t *testing.T) {
	h := fakehost.New(download)
	installed(h, map[string]string{"OMNISMITH_ACCESS_TOKEN": secret, "OMNISMITH_PROJECT_ID": "p1", "HTTPS_PROXY": "http://old:3128"})
	h.State.DropInPaths = []string{"/etc/systemd/system/omnistat.service.d/override.conf"}
	pc := &precheck{}
	p, err := systemd.PlanInstall(context.Background(), h, opts(pc, map[string]string{"HTTPS_PROXY": "http://new:3128"}))
	if err != nil {
		t.Fatal(err)
	}
	if s, l := h.Prompts(); !p.Upgrade || s+l != 0 || pc.env["OMNISMITH_ACCESS_TOKEN"] != secret {
		t.Fatalf("upgrade %t, prompts %d, pre-check %v", p.Upgrade, s+l, pc.env)
	}
	if d := describe(p); !strings.Contains(d, "replace /usr/local/bin/omnistat with "+download) || !strings.Contains(d, "override.conf") {
		t.Fatalf("dry-run:\n%s", d)
	}
	apply(t, p)
	calls := h.Calls()
	if calls[0] != "systemctl stop omnistat.service" || calls[len(calls)-1] != "systemctl start omnistat.service" {
		t.Fatalf("stop first, start last: %v", calls)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "CreateFile") || strings.HasPrefix(c, "Secure") || strings.HasPrefix(c, "MkdirAll /etc/omnistat") {
			t.Fatalf("the config is left alone: %v", calls)
		}
	}
	if c, _ := h.File(systemd.ConfigPath); string(c.Data) != "log: {level: debug}\n" {
		t.Fatal("config changed")
	}
	e, _ := h.File(systemd.EnvPath)
	stored, _ := systemd.ParseEnv(e.Data)
	if stored["OMNISMITH_ACCESS_TOKEN"] != secret || stored["OMNISMITH_PROJECT_ID"] != "p1" || stored["HTTPS_PROXY"] != "http://new:3128" {
		t.Fatalf("merge: %d settings", len(stored))
	}
}

// US-5/2, FR-018: --replace-token asks even with a stored token.
func TestInstall_ReplaceToken(t *testing.T) {
	h := fakehost.New(download)
	installed(h, map[string]string{"OMNISMITH_ACCESS_TOKEN": "omni_old", "OMNISMITH_PROJECT_ID": "p1"})
	h.Secret = secret
	o := opts(&precheck{}, nil)
	o.ReplaceToken = true
	p, err := systemd.PlanInstall(context.Background(), h, o)
	if s, _ := h.Prompts(); err != nil || s != 1 {
		t.Fatalf("%v prompts %d", err, s)
	}
	apply(t, p)
	e, _ := h.File(systemd.EnvPath)
	if stored, _ := systemd.ParseEnv(e.Data); stored["OMNISMITH_ACCESS_TOKEN"] != secret {
		t.Fatal("token replaced")
	}
}

// Edge case: install run from the installed copy has nothing to copy.
func TestInstall_FromInstalledBinary(t *testing.T) {
	h := fakehost.New(systemd.BinaryPath)
	installed(h, apiEnv())
	p, err := systemd.PlanInstall(context.Background(), h, opts(&precheck{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	apply(t, p)
	for _, c := range h.Calls() {
		if strings.HasPrefix(c, "WriteFile "+systemd.BinaryPath) {
			t.Fatalf("nothing to copy: %v", h.Calls())
		}
	}
}

// FR-019: a unit omnistat did not write is left alone, and its path named.
func TestInstall_ForeignUnit(t *testing.T) {
	for name, tc := range map[string]struct {
		mod  func(*fakehost.Host)
		path string
	}{
		"hand-written": {func(h *fakehost.Host) {
			h.Files[systemd.UnitPath] = fakehost.Entry{Data: []byte("[Service]\nExecStart=/opt/omnistat run --daemon\n"), Mode: 0o644}
			h.State = systemd.Unit{LoadState: "loaded", FragmentPath: systemd.UnitPath, ActiveState: "active"}
		}, systemd.UnitPath},
		"from a package": {func(h *fakehost.Host) {
			h.State = systemd.Unit{LoadState: "loaded", FragmentPath: "/usr/lib/systemd/system/omnistat.service", ActiveState: "inactive"}
		}, "/usr/lib/systemd/system/omnistat.service"},
		"masked": {func(h *fakehost.Host) {
			h.State = systemd.Unit{LoadState: "masked", FragmentPath: systemd.UnitPath, ActiveState: "inactive"}
		}, "masked"},
	} {
		t.Run(name, func(t *testing.T) {
			h := fakehost.New(download)
			tc.mod(h)
			pc := &precheck{}
			_, err := systemd.PlanInstall(context.Background(), h, opts(pc, apiEnv()))
			if !errors.Is(err, systemd.ErrForeignUnit) || !strings.Contains(err.Error(), tc.path) || len(h.Calls()) != 0 || pc.calls != 0 {
				t.Fatalf("%v %v", err, h.Calls())
			}
		})
	}
}

// FR-012: a config directory or file that others could write, that root does
// not own, or that the service could not read is fixed, and the dry-run says so.
func TestInstall_FixesLoosePermissions(t *testing.T) {
	for name, tc := range map[string]struct {
		dir, file fakehost.Entry
		want      []string
	}{
		"writable by all": {fakehost.Entry{Dir: true, Mode: 0o777}, fakehost.Entry{Mode: 0o666},
			[]string{"Secure /etc/omnistat 0755", "Secure /etc/omnistat/omnistat.yaml 0644"}},
		"owned by a user": {fakehost.Entry{Dir: true, Mode: 0o755, UID: 1000}, fakehost.Entry{Mode: 0o644, UID: 1000},
			[]string{"Secure /etc/omnistat 0755", "Secure /etc/omnistat/omnistat.yaml 0644"}},
		"unreadable by the service": {fakehost.Entry{Dir: true, Mode: 0o750}, fakehost.Entry{Mode: 0o600},
			[]string{"Secure /etc/omnistat 0755", "Secure /etc/omnistat/omnistat.yaml 0644"}},
		"already right": {fakehost.Entry{Dir: true, Mode: 0o755}, fakehost.Entry{Mode: 0o640 | 0o004}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			h := fakehost.New(download)
			h.Files[systemd.ConfigDir], h.Files[systemd.ConfigPath] = tc.dir, tc.file
			p, err := systemd.PlanInstall(context.Background(), h, opts(&precheck{}, apiEnv()))
			if err != nil {
				t.Fatal(err)
			}
			d := describe(p)
			apply(t, p)
			var got []string
			for _, c := range h.Calls() {
				if strings.HasPrefix(c, "Secure") {
					got = append(got, c)
				}
				if strings.HasPrefix(c, "CreateFile") {
					t.Fatalf("an existing config is never rewritten: %v", h.Calls())
				}
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("fixes %v, want %v", got, tc.want)
			}
			if len(tc.want) > 0 && !strings.Contains(d, "fix /etc/omnistat") {
				t.Fatalf("dry-run should say what it fixes:\n%s", d)
			}
		})
	}
}

// FR-011: a service that fails to start, or fails right after, fails the
// install and points to the journal.
func TestInstall_StartFails(t *testing.T) {
	for name, mod := range map[string]func(*fakehost.Host){
		"start fails":     func(h *fakehost.Host) { h.Fail = map[string]error{"systemctl start": errors.New("exit status 1")} },
		"restarting":      func(h *fakehost.Host) { h.AfterStart = [2]string{"activating", "auto-restart"} },
		"failed at once":  func(h *fakehost.Host) { h.AfterStart = [2]string{"failed", "failed"} },
		"stopped at once": func(h *fakehost.Host) { h.AfterStart = [2]string{"inactive", "dead"} },
	} {
		t.Run(name, func(t *testing.T) {
			h := fakehost.New(download)
			mod(h)
			p, err := systemd.PlanInstall(context.Background(), h, opts(&precheck{}, apiEnv()))
			if err != nil {
				t.Fatal(err)
			}
			err = p.Apply(context.Background(), &bytes.Buffer{})
			if !errors.Is(err, systemd.ErrStartFailed) || !strings.Contains(err.Error(), "journalctl -u omnistat") {
				t.Fatalf("%v", err)
			}
		})
	}
}

// Spec edge case: a stored settings file install cannot read stops it before
// any change, naming the file and the line, never the content.
func TestInstall_UnreadableSettings(t *testing.T) {
	h := fakehost.New(download)
	installed(h, apiEnv())
	h.Files[systemd.EnvPath] = fakehost.Entry{Data: []byte("OMNISMITH_PROJECT_ID='p1'\n" + secret + "\n"), Mode: 0o600}
	_, err := systemd.PlanInstall(context.Background(), h, opts(&precheck{}, nil))
	if err == nil || !strings.Contains(err.Error(), systemd.EnvPath) || !strings.Contains(err.Error(), "line 2") || len(h.Calls()) != 0 {
		t.Fatalf("%v %v", err, h.Calls())
	}
	noSecrets(t, err.Error())
}
