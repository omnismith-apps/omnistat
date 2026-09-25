package winsvc_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// FR-020: uninstall needs elevation.
func TestUninstall_NotElevated(t *testing.T) {
	h := newHost()
	h.IsElevated = false
	if _, err := winsvc.PlanUninstall(context.Background(), h); !errors.Is(err, winsvc.ErrNotElevated) || len(h.Calls()) != 0 {
		t.Fatalf("%v %v", err, h.Calls())
	}
}

// US-6/2: nothing installed → say so, change nothing.
func TestUninstall_NotInstalled(t *testing.T) {
	h := newHost()
	p, err := winsvc.PlanUninstall(context.Background(), h)
	if err != nil || !p.NotInstalled || len(p.Steps) != 0 || len(h.Calls()) != 0 {
		t.Fatalf("%+v %v %v", p, err, h.Calls())
	}
}

// FR-019 applies to uninstall too: never delete someone else's service.
func TestUninstall_ForeignService(t *testing.T) {
	h := newHost()
	h.Svc = winsvc.Installed{Exists: true, Binary: `D:\vendor\agent\omnistat.exe`}
	if _, err := winsvc.PlanUninstall(context.Background(), h); !errors.Is(err, winsvc.ErrForeignService) || len(h.Calls()) != 0 {
		t.Fatalf("%v %v", err, h.Calls())
	}
}

// US-6/1, FR-020: stop (final publish), delete the service and its settings,
// remove the event source and the binary; keep the config directory.
func TestUninstall_Full(t *testing.T) {
	h := newHost()
	h.Svc = winsvc.Installed{Exists: true, Binary: installed, State: winsvc.StateRunning, Env: map[string]string{"OMNISMITH_ACCESS_TOKEN": secret}}
	p, err := winsvc.PlanUninstall(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	if d := descs(p); len(h.Calls()) != 0 || !strings.Contains(d, installed) || strings.Contains(d, secret) {
		t.Fatalf("dry-run: calls %v\n%s", h.Calls(), d)
	}
	var out bytes.Buffer
	if err := p.Apply(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"Stop", "Delete", "EventSource false", "RemoveBinary " + installed}
	if got := h.Calls(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v, want %v", got, want)
	}
	if !strings.Contains(out.String(), "kept "+cfgDir) {
		t.Fatalf("must say the config directory is kept:\n%s", out.String())
	}
	for _, c := range h.Calls() {
		if strings.Contains(c, cfgDir) {
			t.Fatalf("config directory touched: %v", h.Calls())
		}
	}
}

// FR-020: the running binary cannot delete itself. It is moved out of the
// program directory, which goes at once; the report says what remains until
// the next restart and where, never the installed path itself, so a reinstall
// before that restart cannot lose its binary.
func TestUninstall_BinaryInUse(t *testing.T) {
	h := newHost()
	h.Exe = installed
	h.Svc = winsvc.Installed{Exists: true, Binary: installed, State: winsvc.StateStopped}
	leftover := `C:\Windows\Temp\omnistat-uninstalled-4242.exe`
	h.InUse = map[string]string{installed: leftover}
	p, err := winsvc.PlanUninstall(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Apply(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	_, note, found := strings.Cut(out.String(), "done: remove "+installed)
	if !found {
		t.Fatalf("no remove step:\n%s", out.String())
	}
	if !strings.Contains(note, leftover) || !strings.Contains(note, "next restart") || !strings.Contains(note, progDir+" is removed") {
		t.Fatalf("must say what remains, where, and that the program directory is gone:\n%s", out.String())
	}
	if h.Calls()[0] == "Stop" {
		t.Fatal("a stopped service is not stopped again")
	}
}

// When the running binary can only be renamed next to itself, the directory
// stays until the restart too, and the report says so.
func TestUninstall_BinaryRenamedInPlace(t *testing.T) {
	h := newHost()
	h.Svc = winsvc.Installed{Exists: true, Binary: installed, State: winsvc.StateStopped}
	h.InUse = map[string]string{installed: progDir + `\omnistat-uninstalled-4242.exe`}
	p, err := winsvc.PlanUninstall(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Apply(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "renamed to "+progDir+`\omnistat-uninstalled-4242.exe`) || !strings.Contains(out.String(), "deletes it and "+progDir+" at the next restart") {
		t.Fatalf("%s", out.String())
	}
}
