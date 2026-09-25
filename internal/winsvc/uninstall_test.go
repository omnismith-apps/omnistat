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

// FR-020: the running binary cannot delete itself; say what remains.
func TestUninstall_BinaryInUse(t *testing.T) {
	h := newHost()
	h.Exe = installed
	h.Svc = winsvc.Installed{Exists: true, Binary: installed, State: winsvc.StateStopped}
	h.InUse = map[string]bool{installed: true}
	p, err := winsvc.PlanUninstall(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Apply(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), installed) || !strings.Contains(out.String(), "next restart") {
		t.Fatalf("must say what remains and when it goes:\n%s", out.String())
	}
	if h.Calls()[0] == "Stop" {
		t.Fatal("a stopped service is not stopped again")
	}
}
