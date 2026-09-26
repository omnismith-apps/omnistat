package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/systemd"
	sdfake "github.com/omnismith-apps/omnistat/internal/systemd/fakehost"
	"github.com/omnismith-apps/omnistat/internal/upgrade/upgradetest"
	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// Spec 009 through the CLI: the fake service hosts say what is installed, an
// httptest server is the releases location, and a fake runner stands in for
// the installed and the downloaded binaries. A downloaded "binary" is the
// text its `version` prints.

// fakeRunner answers `version` for the installed binary from Installed, and
// for any other binary from the file's content; it records the install.
type fakeRunner struct {
	installedPath string
	Installed     string
	InstalledErr  error
	Code          int
	InstallErr    error
	calls         [][]string
	staged        string // content of the binary install was run with
}

func (r *fakeRunner) Version(_ context.Context, bin string) (string, error) {
	if bin == r.installedPath {
		return r.Installed, r.InstalledErr
	}
	data, err := os.ReadFile(bin) //nolint:gosec // test file
	return string(data), err
}

func (r *fakeRunner) Install(bin string, args []string, stdout, _ io.Writer) (int, error) {
	r.calls = append(r.calls, args)
	data, _ := os.ReadFile(bin) //nolint:gosec // test file
	r.staged = string(data)
	fmt.Fprintln(stdout, "(the new version's install report)")
	return r.Code, r.InstallErr
}

type upgradeCase struct {
	srv    *upgradetest.Server
	runner *fakeRunner
	stage  string
	env    map[string]string
}

// newUpgradeCase publishes v0.3.0 and v0.4.0 (latest) for linux and windows
// on this CPU; each archive's binary reports its own version.
func newUpgradeCase(t *testing.T, installedPath, installed string) *upgradeCase {
	t.Helper()
	srv := upgradetest.New()
	t.Cleanup(srv.Close)
	for _, v := range []string{"v0.3.0", "v0.4.0", "v0.5.0-rc.1"} {
		srv.Add(v, []byte("omnistat "+v+"\n"), "linux/"+runtime.GOARCH, "windows/"+runtime.GOARCH)
	}
	srv.Latest = "v0.4.0"
	return &upgradeCase{
		srv:    srv,
		runner: &fakeRunner{installedPath: installedPath, Installed: "omnistat " + installed + "\n"},
		stage:  t.TempDir(),
		env:    map[string]string{"OMNISTAT_RELEASES_URL": srv.URL},
	}
}

// installedSystemd is a Linux host with omnistat's service installed and
// secrets among its stored settings.
func installedSystemd(t *testing.T) *sdfake.Host {
	t.Helper()
	h := sdfake.New(linuxDownload)
	data, err := systemd.FormatEnv(map[string]string{"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "HTTPS_PROXY": proxySecret})
	if err != nil {
		t.Fatal(err)
	}
	h.Files[systemd.BinaryPath] = sdfake.Entry{Data: []byte("old"), Mode: 0o755}
	h.Files[systemd.EnvPath] = sdfake.Entry{Data: data, Mode: 0o600}
	h.Files[systemd.UnitPath] = sdfake.Entry{Data: []byte(systemd.UnitText()), Mode: 0o644}
	h.State = systemd.Unit{LoadState: "loaded", FragmentPath: systemd.UnitPath, ActiveState: "active", SubState: "running"}
	return h
}

func (c *upgradeCase) run(t *testing.T, app *cli.App, args ...string) run {
	t.Helper()
	// The systemd backend exists only on Linux, and the platform picks the
	// archive: pin it, since these tests run on Windows in CI too.
	if app.SystemdHost != nil && app.GOOS == "" {
		app.GOOS = "linux"
	}
	app.Registry = registryWithMachineID(fstest.MapFS{})
	app.Version = "t"
	app.Runner = c.runner
	app.StageDir = c.stage
	var out, errb bytes.Buffer
	code := app.Run(context.Background(), append([]string{"upgrade"}, args...), &out, &errb, func(k string) string { return c.env[k] })
	r := run{code, out.String(), errb.String()}
	assertNoSecrets(t, r)
	if entries, _ := os.ReadDir(c.stage); len(entries) != 0 {
		t.Fatalf("the staging directory was not cleaned up (FR-011): %d entries", len(entries))
	}
	return r
}

func contains(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("output should contain %q:\n%s", w, text)
		}
	}
}

// US-1/1, FR-009, FR-013, FR-014, FR-016: verified download, handed over,
// summary; stored secrets never printed.
func TestUpgrade_Linux(t *testing.T) {
	c := newUpgradeCase(t, systemd.BinaryPath, "v0.3.0")
	r := c.run(t, &cli.App{SystemdHost: installedSystemd(t)})
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	asset := fmt.Sprintf("omnistat_0.4.0_linux_%s.tar.gz", runtime.GOARCH)
	contains(t, r.stdout,
		"installed: v0.3.0 (/usr/local/bin/omnistat)",
		"target:    v0.4.0 (the latest release)",
		"download:  "+c.srv.URL+"/download/v0.4.0/"+asset,
		"verified:  SHA-256 matches "+c.srv.URL+"/latest/download/checksums.txt; the binary reports v0.4.0",
		"Handing over to omnistat v0.4.0: service install",
		"(the new version's install report)",
		"omnistat is upgraded from v0.3.0 to v0.4.0.",
	)
	if len(c.runner.calls) != 1 || !slices.Equal(c.runner.calls[0], []string{"service", "install"}) || c.runner.staged != "omnistat v0.4.0\n" {
		t.Fatalf("handover: %v with %q", c.runner.calls, c.runner.staged)
	}
}

// US-3/1, FR-013: the dry-run hands over to the new install's dry-run.
func TestUpgrade_DryRun(t *testing.T) {
	c := newUpgradeCase(t, systemd.BinaryPath, "v0.3.0")
	r := c.run(t, &cli.App{SystemdHost: installedSystemd(t)}, "--dry-run")
	if r.code != 0 || len(c.runner.calls) != 1 || !slices.Equal(c.runner.calls[0], []string{"service", "install", "--dry-run"}) {
		t.Fatalf("%+v %v", r, c.runner.calls)
	}
	contains(t, r.stdout, "dry run, nothing is changed", "Dry run: nothing was changed")
}

// US-1/2, US-2, FR-007, FR-017: nothing is downloaded when there is nothing
// to do, and --check never downloads.
func TestUpgrade_NothingToDo(t *testing.T) {
	for _, tc := range []struct {
		name, installed string
		args            []string
		code            int
		want            string
	}{
		{"up to date", "v0.4.0", nil, 0, "omnistat is up to date (v0.4.0); nothing to do."},
		{"installed newer than latest", "v0.5.0-rc.1", nil, 0, "The installed v0.5.0-rc.1 is newer than the latest release v0.4.0"},
		{"check: available", "v0.3.0", []string{"--check"}, 2, "An upgrade is available: run `omnistat upgrade` with the same rights to install v0.4.0."},
		{"check: pinned", "v0.4.0", []string{"--check", "--version", "v0.5.0-rc.1"}, 2, "run `omnistat upgrade --version v0.5.0-rc.1`"},
		{"check: up to date", "v0.4.0", []string{"--check"}, 0, "up to date"},
		{"check: dev build", "v0.3.0-3-gd38c574-dirty", []string{"--check"}, 2, `reports "omnistat v0.3.0-3-gd38c574-dirty", not a release version`},
		{"same version pinned", "v0.3.0", []string{"--version", "0.3.0"}, 0, "up to date (v0.3.0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newUpgradeCase(t, systemd.BinaryPath, tc.installed)
			r := c.run(t, &cli.App{SystemdHost: installedSystemd(t)}, tc.args...)
			if r.code != tc.code || c.srv.Downloaded() || len(c.runner.calls) != 0 {
				t.Fatalf("code %d (want %d), downloaded %v, installs %v\n%+v", r.code, tc.code, c.srv.Downloaded(), c.runner.calls, r)
			}
			contains(t, r.stdout, tc.want)
		})
	}
}

// FR-005: an installed binary that does not run or is a development build is
// an unknown version: any release is newer.
func TestUpgrade_UnknownInstalled(t *testing.T) {
	c := newUpgradeCase(t, systemd.BinaryPath, "")
	c.runner.InstalledErr = errors.New("exec format error")
	r := c.run(t, &cli.App{SystemdHost: installedSystemd(t)})
	if r.code != 0 || len(c.runner.calls) != 1 {
		t.Fatalf("%+v", r)
	}
	contains(t, r.stdout, "installed: unknown — /usr/local/bin/omnistat did not report its version (exec format error)", "omnistat is upgraded to v0.4.0.")
}

// US-4, FR-006, FR-007: a pinned older release is a downgrade; a pre-release
// only when named.
func TestUpgrade_Pinned(t *testing.T) {
	c := newUpgradeCase(t, systemd.BinaryPath, "v0.4.0")
	r := c.run(t, &cli.App{SystemdHost: installedSystemd(t)}, "--version", "v0.3.0")
	if r.code != 0 || c.runner.staged != "omnistat v0.3.0\n" {
		t.Fatalf("%+v %q", r, c.runner.staged)
	}
	contains(t, r.stdout, "target:    v0.3.0 (--version)", "this is a downgrade", "upgraded from v0.4.0 to v0.3.0")

	c = newUpgradeCase(t, systemd.BinaryPath, "v0.4.0")
	r = c.run(t, &cli.App{SystemdHost: installedSystemd(t)}, "--version", "v0.5.0-rc.1")
	if r.code != 0 || c.runner.staged != "omnistat v0.5.0-rc.1\n" {
		t.Fatalf("%+v", r)
	}

	c = newUpgradeCase(t, systemd.BinaryPath, "v0.4.0")
	r = c.run(t, &cli.App{SystemdHost: installedSystemd(t)}, "--version", "v9.9.9")
	if r.code != 1 || !strings.Contains(r.stderr, "release v9.9.9 not found") {
		t.Fatalf("%+v", r)
	}
}

// US-1/3, FR-009: a download that fails verification never reaches install.
func TestUpgrade_VerificationFails(t *testing.T) {
	c := newUpgradeCase(t, systemd.BinaryPath, "v0.3.0")
	c.srv.BadSum["v0.4.0"] = true
	r := c.run(t, &cli.App{SystemdHost: installedSystemd(t)})
	if r.code != 1 || len(c.runner.calls) != 0 {
		t.Fatalf("%+v", r)
	}
	contains(t, r.stderr, "does not match its SHA-256 in checksums.txt", "nothing was changed")

	c = newUpgradeCase(t, systemd.BinaryPath, "v0.3.0")
	c.srv.Add("v0.4.0", []byte("omnistat v0.9.9\n"), "linux/"+runtime.GOARCH) // a mislabelled build
	r = c.run(t, &cli.App{SystemdHost: installedSystemd(t)})
	if r.code != 1 || len(c.runner.calls) != 0 {
		t.Fatalf("%+v", r)
	}
	contains(t, r.stderr, `the downloaded binary reports "omnistat v0.9.9", not v0.4.0; nothing was changed`)
}

// US-1/4, FR-014: the install's failure status is upgrade's.
func TestUpgrade_InstallFails(t *testing.T) {
	c := newUpgradeCase(t, systemd.BinaryPath, "v0.3.0")
	c.runner.Code = 1
	r := c.run(t, &cli.App{SystemdHost: installedSystemd(t)})
	if r.code != 1 || strings.Contains(r.stdout, "upgraded") {
		t.Fatalf("%+v", r)
	}
	contains(t, r.stderr, "upgrade to v0.4.0 did not complete: `service install` exited with status 1")
}

// FR-001–FR-004, FR-015: refusals before any network access.
func TestUpgrade_Refused(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  func(t *testing.T) *cli.App
		args []string
		env  map[string]string
		want string
	}{
		{name: "check with dry-run", args: []string{"--check", "--dry-run"}, want: "usage: omnistat upgrade",
			app: func(t *testing.T) *cli.App { return &cli.App{SystemdHost: installedSystemd(t)} }},
		{name: "not root", want: "run it with sudo", app: func(t *testing.T) *cli.App {
			h := installedSystemd(t)
			h.IsRoot = false
			return &cli.App{SystemdHost: h}
		}},
		{name: "not installed", want: "omnistat is not installed as a service on this host",
			app: func(*testing.T) *cli.App { return &cli.App{SystemdHost: sdfake.New(linuxDownload)} }},
		{name: "macOS", want: "upgrade: not supported on darwin",
			app: func(*testing.T) *cli.App { return &cli.App{GOOS: "darwin"} }},
		{name: "plain http mirror", env: map[string]string{"OMNISTAT_RELEASES_URL": "http://mirror.example/omnistat"}, want: "must be an https:// URL",
			app: func(t *testing.T) *cli.App { return &cli.App{SystemdHost: installedSystemd(t)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newUpgradeCase(t, systemd.BinaryPath, "v0.3.0")
			for k, v := range tc.env {
				c.env[k] = v
			}
			r := c.run(t, tc.app(t), tc.args...)
			if r.code != 1 || len(c.srv.Hits()) != 0 {
				t.Fatalf("code %d, hits %v\n%+v", r.code, c.srv.Hits(), r)
			}
			contains(t, r.stderr, tc.want)
		})
	}
}

// US-2/3: an unreachable releases location is an error.
func TestUpgrade_Unreachable(t *testing.T) {
	c := newUpgradeCase(t, systemd.BinaryPath, "v0.3.0")
	c.srv.Close()
	r := c.run(t, &cli.App{SystemdHost: installedSystemd(t)}, "--check")
	if r.code != 1 || !strings.Contains(r.stderr, "reading the release's checksums") {
		t.Fatalf("%+v", r)
	}
}

// The Windows flow: the zip, the service's registered binary, the handover.
func TestUpgrade_Windows(t *testing.T) {
	installedExe := `C:\Program Files\omnistat\omnistat.exe`
	h := serviceHost(t)
	h.Svc = winsvc.Installed{Exists: true, Binary: installedExe, State: winsvc.StateRunning,
		Env: map[string]string{"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "HTTPS_PROXY": proxySecret}}
	c := newUpgradeCase(t, installedExe, "v0.3.0")
	r := c.run(t, &cli.App{ServiceHost: h, GOOS: "windows"})
	if r.code != 0 || c.runner.staged != "omnistat v0.4.0\n" {
		t.Fatalf("%+v", r)
	}
	contains(t, r.stdout, `installed: v0.3.0 (C:\Program Files\omnistat\omnistat.exe)`,
		fmt.Sprintf("omnistat_0.4.0_windows_%s.zip", runtime.GOARCH), "upgraded from v0.3.0 to v0.4.0")
	if calls := h.Calls(); len(calls) != 0 {
		t.Fatalf("upgrade itself must not touch the service: %v", calls)
	}

	h.IsElevated = false
	r = c.run(t, &cli.App{ServiceHost: h, GOOS: "windows"})
	if r.code != 1 || !strings.Contains(r.stderr, "Administrator") {
		t.Fatalf("%+v", r)
	}
}

// FR-011: the default staging directory is the installed binary's. The
// Windows backend joins paths with a backslash, so this runs on Windows (CI);
// on Linux the container acceptance checks it (noexec /tmp).
func TestUpgrade_StagesNextToTheBinary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths; runs in CI")
	}
	dir := t.TempDir()
	installedExe := filepath.Join(dir, "omnistat.exe")
	h := serviceHost(t)
	h.ProgDir = dir
	h.Svc = winsvc.Installed{Exists: true, Binary: installedExe, State: winsvc.StateRunning}
	c := newUpgradeCase(t, installedExe, "v0.3.0")
	var staged string
	app := &cli.App{ServiceHost: h, GOOS: "windows", Registry: registryWithMachineID(fstest.MapFS{}), Runner: &stageSpy{fakeRunner: c.runner, dir: &staged}}
	var out, errb bytes.Buffer
	code := app.Run(context.Background(), []string{"upgrade"}, &out, &errb, func(k string) string { return c.env[k] })
	if code != 0 || filepath.Dir(filepath.Dir(staged)) != dir || !strings.HasPrefix(filepath.Base(filepath.Dir(staged)), ".omnistat-upgrade-") {
		t.Fatalf("%d staged at %q\n%s%s", code, staged, out.String(), errb.String())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left %d entries next to the binary", len(entries))
	}
}

type stageSpy struct {
	*fakeRunner
	dir *string
}

func (s *stageSpy) Install(bin string, args []string, stdout, stderr io.Writer) (int, error) {
	*s.dir = bin
	return s.fakeRunner.Install(bin, args, stdout, stderr)
}
