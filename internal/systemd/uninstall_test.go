package systemd_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/systemd"
	"github.com/omnismith-apps/omnistat/internal/systemd/fakehost"
)

// FR-020: root and systemd first, as for install.
func TestUninstall_Preconditions(t *testing.T) {
	h := fakehost.New(download)
	h.IsRoot = false
	if _, err := systemd.PlanUninstall(context.Background(), h); !errors.Is(err, systemd.ErrNotRoot) || len(h.Calls()) != 0 {
		t.Fatalf("%v", err)
	}
	h = fakehost.New(download)
	h.IsBooted = false
	if _, err := systemd.PlanUninstall(context.Background(), h); !errors.Is(err, systemd.ErrNotBooted) || len(h.Calls()) != 0 {
		t.Fatalf("%v", err)
	}
}

// US-6/2: nothing installed → nothing to do.
func TestUninstall_NotInstalled(t *testing.T) {
	h := fakehost.New(download)
	p, err := systemd.PlanUninstall(context.Background(), h)
	if err != nil || !p.NotInstalled || len(h.Calls()) != 0 {
		t.Fatalf("%+v %v", p, err)
	}
}

// FR-020, FR-019: a unit omnistat did not write is not removed.
func TestUninstall_ForeignUnit(t *testing.T) {
	h := fakehost.New(download)
	h.Files[systemd.UnitPath] = fakehost.Entry{Data: []byte("[Service]\nExecStart=/opt/x\n"), Mode: 0o644}
	h.State = systemd.Unit{LoadState: "loaded", FragmentPath: systemd.UnitPath, ActiveState: "active"}
	if _, err := systemd.PlanUninstall(context.Background(), h); !errors.Is(err, systemd.ErrForeignUnit) || len(h.Calls()) != 0 {
		t.Fatalf("%v %v", err, h.Calls())
	}
}

// US-6/1, US-6/3, FR-020, FR-021: stop, disable, then the settings (token)
// and binary before the unit, so an interrupted uninstall is finished by
// running it again; the config and the admin's drop-ins stay. The dry-run
// changes nothing.
func TestUninstall(t *testing.T) {
	h := fakehost.New(download)
	installed(h, apiEnv())
	h.State.DropInPaths = []string{"/etc/systemd/system/omnistat.service.d/override.conf"}

	p, err := systemd.PlanUninstall(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	d := describe(p)
	for _, want := range []string{"stop the service", "disable", systemd.EnvPath, systemd.BinaryPath, systemd.UnitPath,
		"would keep /etc/omnistat", "omnistat.yaml", "would keep /etc/systemd/system/omnistat.service.d/override.conf"} {
		if !strings.Contains(d, want) {
			t.Errorf("dry-run should show %q:\n%s", want, d)
		}
	}
	if len(h.Calls()) != 0 {
		t.Fatalf("dry-run changed something: %v", h.Calls())
	}

	out := apply(t, p)
	want := []string{
		"systemctl stop omnistat.service",
		"systemctl disable omnistat.service",
		"systemctl reset-failed omnistat.service",
		"Remove /etc/omnistat/omnistat.env",
		"Remove /usr/local/bin/omnistat",
		"Remove /etc/systemd/system/omnistat.service",
		"systemctl daemon-reload",
	}
	if got := h.Calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, gone := range []string{systemd.EnvPath, systemd.BinaryPath, systemd.UnitPath} {
		if _, ok := h.File(gone); ok {
			t.Errorf("%s should be removed", gone)
		}
	}
	for _, kept := range []string{systemd.ConfigDir, systemd.ConfigPath} {
		if _, ok := h.File(kept); !ok {
			t.Errorf("%s should be kept", kept)
		}
	}
	if !strings.Contains(out, "kept /etc/omnistat") {
		t.Fatalf("the output says what was kept:\n%s", out)
	}
	noSecrets(t, d, out)
}

// A stopped service is not stopped again; a failing reset-failed (nothing to
// reset) does not stop the uninstall.
func TestUninstall_StoppedService(t *testing.T) {
	h := fakehost.New(download)
	installed(h, apiEnv())
	h.State.ActiveState, h.State.SubState = "inactive", "dead"
	h.Fail = map[string]error{"systemctl reset-failed": errors.New("unit not loaded")}
	p, err := systemd.PlanUninstall(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, p)
	if c := h.Calls(); c[0] != "systemctl disable omnistat.service" {
		t.Fatalf("%v", c)
	}
}
