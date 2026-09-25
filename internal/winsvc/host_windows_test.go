//go:build windows

package winsvc

import (
	"context"
	"strings"
	"testing"
)

// Read-only smoke test of the real Host on the CI runner (spec 006 NFR-003):
// known folders resolve, and querying a service that is not installed works.
// Nothing here installs or changes anything.
func TestWindowsHost_ReadOnly(t *testing.T) {
	h, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	prog, err := h.ProgramDir()
	if err != nil || !strings.HasSuffix(strings.ToLower(prog), `\program files\omnistat`) {
		t.Fatalf("program dir %q %v", prog, err)
	}
	cfg, err := h.ConfigDir()
	if err != nil || !strings.HasSuffix(strings.ToLower(cfg), `\programdata\omnistat`) {
		t.Fatalf("config dir %q %v", cfg, err)
	}
	if !h.Elevated() {
		t.Skip("service manager queries need an elevated runner")
	}
	inst, err := h.Service(context.Background())
	if err != nil || inst.Exists {
		t.Fatalf("no omnistat service on a CI runner: %+v %v", inst, err)
	}
}

func TestExeOf(t *testing.T) {
	for in, want := range map[string]string{
		`"C:\Program Files\omnistat\omnistat.exe" run --daemon`: `C:\Program Files\omnistat\omnistat.exe`,
		`C:\tools\omnistat.exe run --daemon`:                    `C:\tools\omnistat.exe`,
		`C:\Program Files\x\omnistat.EXE --flag`:                `C:\Program Files\x\omnistat.EXE`,
	} {
		if got := exeOf(in); got != want {
			t.Errorf("exeOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// commandLine must match what CreateService writes, so an update does not
// change the command line of a service it just created.
func TestCommandLine(t *testing.T) {
	got := commandLine(`C:\Program Files\omnistat\omnistat.exe`, Args)
	if got != `"C:\Program Files\omnistat\omnistat.exe" run --daemon` {
		t.Fatalf("%s", got)
	}
}
