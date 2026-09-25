//go:build linux

package systemd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/systemd"
)

func realHost(t *testing.T) systemd.Host {
	t.Helper()
	h, err := systemd.NewHost()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// The real host's file operations, on a temporary directory: WriteFile
// replaces atomically with the exact mode, whatever the umask; CreateFile
// never clobbers; MkdirAll sets the mode of what it creates; Stat and Remove
// treat a missing path as absent.
func TestRealHost_Files(t *testing.T) {
	h := realHost(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "omnistat.env")

	if err := h.WriteFile(path, []byte("A='1'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.WriteFile(path, []byte("A='2'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := h.Stat(path)
	if err != nil || !f.Exists || f.Dir || f.Mode != 0o600 || f.UID != os.Geteuid() {
		t.Fatalf("%+v %v", f, err)
	}
	if b, _ := h.ReadFile(path); string(b) != "A='2'\n" {
		t.Fatalf("%q", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("no temporary file is left behind: %v", entries)
	}

	created, err := h.CreateFile(path, []byte("clobbered"), 0o644)
	if err != nil || created {
		t.Fatalf("an existing file is left alone: %t %v", created, err)
	}
	starter := filepath.Join(dir, "omnistat.yaml")
	if created, err := h.CreateFile(starter, []byte("# x\n"), 0o644); err != nil || !created {
		t.Fatalf("%t %v", created, err)
	}
	if f, _ := h.Stat(starter); f.Mode != 0o644 {
		t.Fatalf("mode %o", f.Mode)
	}

	sub := filepath.Join(dir, "a", "etc-omnistat")
	if err := h.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if f, _ := h.Stat(sub); !f.Dir || f.Mode != 0o755 {
		t.Fatalf("%+v", f)
	}

	if f, err := h.Stat(filepath.Join(dir, "missing")); err != nil || f.Exists {
		t.Fatalf("%+v %v", f, err)
	}
	if err := h.Remove(filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
	if err := h.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// FR-012: Secure makes a path root-owned with the given mode. Only root can.
func TestRealHost_Secure(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	h := realHost(t)
	path := filepath.Join(t.TempDir(), "omnistat.yaml")
	if err := os.WriteFile(path, nil, 0o666); err != nil { //nolint:gosec // deliberately loose, then secured
		t.Fatal(err)
	}
	if err := os.Chown(path, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if err := h.Secure(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if f, _ := h.Stat(path); f.UID != 0 || f.Mode != 0o644 {
		t.Fatalf("%+v", f)
	}
}
