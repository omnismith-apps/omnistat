package systemd

import (
	"context"
	"errors"
	"io/fs"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/service"
)

// Errors the commands report.
var (
	ErrUnsupported = errors.New("not supported on this platform")
	ErrNotRoot     = errors.New("needs root: run it with sudo")
	ErrNotBooted   = errors.New("systemd is not running on this host: the service commands need systemd; " +
		"under another init system or supervisor, run `omnistat run --daemon` from it")
	ErrForeignUnit = errors.New("a unit named " + UnitName + " exists that omnistat did not write")
	ErrStartFailed = errors.New("the service did not start, or stopped right after starting; see journalctl -u omnistat")
)

// File is what install needs to know about a path.
type File struct {
	Exists bool
	Dir    bool
	Mode   fs.FileMode // permission bits
	UID    int
}

// Unit is what systemd knows about omnistat.service (systemctl show).
type Unit struct {
	LoadState    string // loaded, not-found, masked, …
	FragmentPath string
	ActiveState  string // active, activating, inactive, failed, …
	SubState     string // running, auto-restart, dead, …
	DropInPaths  []string
}

// Active reports whether the service runs or is on its way to (install and
// uninstall stop it first).
func (u Unit) Active() bool {
	return u.ActiveState == "active" || u.ActiveState == "activating" || u.ActiveState == "reloading" || u.ActiveState == "deactivating"
}

// Host is every call into the OS the Linux feature makes (NFR-004). The real
// one is in host_linux.go; tests use fakehost.
type Host interface {
	// Root reports whether the process runs as root (FR-002).
	Root() bool
	// Booted reports whether systemd is the running init system (FR-004).
	Booted() bool
	// Executable is the path of the running binary.
	Executable() (string, error)
	// Stat describes path, following symlinks; a missing path is not an error.
	Stat(path string) (File, error)
	ReadFile(path string) ([]byte, error)
	// WriteFile replaces path with data, owned by root with mode: written to a
	// temporary file in the same directory, then renamed over path.
	WriteFile(path string, data []byte, mode fs.FileMode) error
	// CreateFile writes data to a new root-owned path with mode, unless path
	// exists; created reports whether it did (FR-014).
	CreateFile(path string, data []byte, mode fs.FileMode) (created bool, err error)
	// MkdirAll creates path (and parents) if missing; a created path gets mode
	// and root ownership exactly.
	MkdirAll(path string, mode fs.FileMode) error
	// Secure makes path owned by root:root with mode (FR-012).
	Secure(path string, mode fs.FileMode) error
	// Remove deletes path; a missing path is not an error.
	Remove(path string) error
	// Unit describes omnistat.service as systemd has it loaded.
	Unit(ctx context.Context) (Unit, error)
	// Systemctl runs systemctl with args.
	Systemctl(ctx context.Context, args ...string) error
	// Prompter reads a missing token (no echo) and project id from the
	// terminal (FR-015, FR-017).
	service.Prompter
}

// ParseShow reads `systemctl show -p …` output into a Unit.
func ParseShow(out string) Unit {
	var u Unit
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "LoadState":
			u.LoadState = v
		case "FragmentPath":
			u.FragmentPath = v
		case "ActiveState":
			u.ActiveState = v
		case "SubState":
			u.SubState = v
		case "DropInPaths":
			if f := strings.Fields(v); len(f) > 0 {
				u.DropInPaths = f
			}
		}
	}
	return u
}
