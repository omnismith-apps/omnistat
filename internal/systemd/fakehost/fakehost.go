// Package fakehost is an in-memory systemd.Host for tests (spec 007 NFR-004).
// Files live in a map; systemctl is simulated just enough for the plans:
// daemon-reload loads or forgets the unit file, start and stop change the
// active state. Every mutating call is recorded in order, so tests can assert
// the sequence install and uninstall apply, and that a dry-run applies nothing.
package fakehost

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/omnismith-apps/omnistat/internal/systemd"
)

// Entry is a file or directory of the fake filesystem.
type Entry struct {
	Data []byte
	Mode fs.FileMode
	UID  int
	Dir  bool
}

// Host is a scriptable systemd.Host. Set the exported fields before use.
type Host struct {
	IsRoot   bool
	IsBooted bool
	Exe      string
	// Files is the filesystem, by absolute path.
	Files map[string]Entry
	// State is omnistat.service as systemd has it loaded.
	State systemd.Unit
	// AfterStart is the active/sub state a start leads to; the zero value
	// means active/running.
	AfterStart [2]string
	// Secret and Line answer the prompts; the errors fail them.
	Secret, Line       string
	SecretErr, LineErr error
	// Fail makes a method (or "systemctl <verb>") return the error.
	Fail map[string]error

	mu             sync.Mutex
	calls          []string
	secrets, lines int
}

var _ systemd.Host = (*Host)(nil)

// New returns a root, systemd-booted host with /etc and /usr/local/bin.
func New(exe string) *Host {
	return &Host{IsRoot: true, IsBooted: true, Exe: exe, Files: map[string]Entry{
		"/etc":           {Dir: true, Mode: 0o755},
		"/usr/local/bin": {Dir: true, Mode: 0o755},
		exe:              {Data: []byte("omnistat binary"), Mode: 0o755, UID: 1000},
	}, State: systemd.Unit{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}}
}

// Calls returns the mutating calls made so far, in order.
func (h *Host) Calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// Prompts returns how many times each prompt was shown.
func (h *Host) Prompts() (secret, line int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.secrets, h.lines
}

// File returns the entry at path.
func (h *Host) File(path string) (Entry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.Files[path]
	return e, ok
}

func (h *Host) record(name, format string, args ...any) error {
	h.calls = append(h.calls, fmt.Sprintf(format, args...))
	return h.Fail[name]
}

// Root implements systemd.Host.
func (h *Host) Root() bool { return h.IsRoot }

// Booted implements systemd.Host.
func (h *Host) Booted() bool { return h.IsBooted }

// Executable implements systemd.Host.
func (h *Host) Executable() (string, error) { return h.Exe, nil }

// Stat implements systemd.Host.
func (h *Host) Stat(path string) (systemd.File, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.Files[path]
	if !ok {
		return systemd.File{}, h.Fail["Stat"]
	}
	return systemd.File{Exists: true, Dir: e.Dir, Mode: e.Mode, UID: e.UID}, h.Fail["Stat"]
}

// ReadFile implements systemd.Host.
func (h *Host) ReadFile(path string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.Files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), e.Data...), h.Fail["ReadFile"]
}

// WriteFile implements systemd.Host.
func (h *Host) WriteFile(path string, data []byte, mode fs.FileMode) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.record("WriteFile", "WriteFile %s %04o", path, mode); err != nil {
		return err
	}
	h.Files[path] = Entry{Data: append([]byte(nil), data...), Mode: mode}
	return nil
}

// CreateFile implements systemd.Host.
func (h *Host) CreateFile(path string, data []byte, mode fs.FileMode) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.record("CreateFile", "CreateFile %s %04o", path, mode); err != nil {
		return false, err
	}
	if _, ok := h.Files[path]; ok {
		return false, nil
	}
	h.Files[path] = Entry{Data: append([]byte(nil), data...), Mode: mode}
	return true, nil
}

// MkdirAll implements systemd.Host.
func (h *Host) MkdirAll(path string, mode fs.FileMode) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.record("MkdirAll", "MkdirAll %s %04o", path, mode); err != nil {
		return err
	}
	if _, ok := h.Files[path]; !ok {
		h.Files[path] = Entry{Dir: true, Mode: mode}
	}
	return nil
}

// Secure implements systemd.Host.
func (h *Host) Secure(path string, mode fs.FileMode) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.record("Secure", "Secure %s %04o", path, mode); err != nil {
		return err
	}
	e := h.Files[path]
	e.Mode, e.UID = mode, 0
	h.Files[path] = e
	return nil
}

// Remove implements systemd.Host.
func (h *Host) Remove(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.record("Remove", "Remove %s", path); err != nil {
		return err
	}
	delete(h.Files, path)
	return nil
}

// Unit implements systemd.Host.
func (h *Host) Unit(context.Context) (systemd.Unit, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.State
	u.DropInPaths = append([]string(nil), u.DropInPaths...)
	return u, h.Fail["Unit"]
}

// Systemctl implements systemd.Host.
func (h *Host) Systemctl(_ context.Context, args ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	if err := h.record("systemctl "+verb, "systemctl %s", strings.Join(args, " ")); err != nil {
		return err
	}
	switch verb {
	case "daemon-reload":
		if _, ok := h.Files[systemd.UnitPath]; ok {
			h.State.LoadState, h.State.FragmentPath = "loaded", systemd.UnitPath
		} else if h.State.FragmentPath == systemd.UnitPath {
			h.State.LoadState, h.State.FragmentPath = "not-found", ""
		}
	case "start", "restart":
		h.State.ActiveState, h.State.SubState = "active", "running"
		if h.AfterStart[0] != "" {
			h.State.ActiveState, h.State.SubState = h.AfterStart[0], h.AfterStart[1]
		}
	case "stop":
		h.State.ActiveState, h.State.SubState = "inactive", "dead"
	}
	return nil
}

// PromptSecret implements systemd.Host.
func (h *Host) PromptSecret(string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.secrets++
	return h.Secret, h.SecretErr
}

// PromptLine implements systemd.Host.
func (h *Host) PromptLine(string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lines++
	return h.Line, h.LineErr
}
