package winsvc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// Spec 009 FR-002, FR-003, FR-012: what upgrade learns about the installed
// service, under install's preconditions, and never by changing anything.
func TestInstallation(t *testing.T) {
	env := map[string]string{"OMNISMITH_ACCESS_TOKEN": secret, "HTTPS_PROXY": "http://proxy:3128"}
	h := newHost()
	h.Svc = winsvc.Installed{Exists: true, Binary: installed, State: winsvc.StateRunning, Env: env}
	in, err := winsvc.Installation(context.Background(), h)
	if err != nil || !in.Exists || in.Binary != installed || in.Settings["HTTPS_PROXY"] != "http://proxy:3128" {
		t.Fatalf("%+v %v", in, err)
	}

	h = newHost()
	if in, err = winsvc.Installation(context.Background(), h); err != nil || in.Exists {
		t.Fatalf("not installed: %+v %v", in, err)
	}

	h = newHost()
	h.IsElevated = false
	if _, err = winsvc.Installation(context.Background(), h); !errors.Is(err, winsvc.ErrNotElevated) {
		t.Fatalf("not elevated: %v", err)
	}

	h = newHost()
	h.Svc = winsvc.Installed{Exists: true, Binary: `C:\tools\omnistat.exe`}
	if _, err = winsvc.Installation(context.Background(), h); !errors.Is(err, winsvc.ErrForeignService) {
		t.Fatalf("foreign: %v", err)
	}
	if calls := h.Calls(); len(calls) != 0 {
		t.Fatalf("changed something: %v", calls)
	}
}
