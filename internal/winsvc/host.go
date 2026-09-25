package winsvc

import (
	"context"
	"errors"
	"time"

	"github.com/omnismith-apps/omnistat/internal/service"
)

// Service registration (spec 006 FR-008–FR-010).
const (
	Name         = "omnistat"
	DisplayName  = "omnistat (Omnismith exporter)"
	Description  = "Publishes this host's identity and readings to an Omnismith project. https://github.com/omnismith-apps/omnistat"
	Account      = `NT SERVICE\omnistat` // virtual account: low privilege, no password (FR-009)
	BinaryName   = "omnistat.exe"
	ConfigFile   = "omnistat.yaml"
	RestartDelay = time.Minute // FR-010
)

// Args is the service's command line after the binary: the daemon (003 FR-019).
var Args = []string{"run", "--daemon"}

// Errors the commands report.
var (
	ErrUnsupported    = errors.New("not supported on this platform")
	ErrNotElevated    = errors.New("needs Administrator rights: run it from an elevated prompt (Run as administrator)")
	ErrForeignService = errors.New("a service named omnistat exists that omnistat did not install")
	ErrStartFailed    = errors.New("the service stopped right after starting; see Event Viewer → Windows Logs → Application, source omnistat")
)

// State is a service's run state, as far as install and uninstall care.
type State int

// Service states.
const (
	StateStopped State = iota
	StateStartPending
	StateRunning
	StateStopPending
	StateOther
)

func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStartPending:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopPending:
		return "stopping"
	}
	return "other"
}

// Installed is what the service manager knows about the omnistat service.
type Installed struct {
	Exists bool
	Binary string            // executable of the registered command line
	State  State             // run state
	Env    map[string]string // stored settings (FR-013)
}

// Registration is the service configuration install writes (FR-008–FR-010).
type Registration struct {
	Binary       string
	Args         []string
	DisplayName  string
	Description  string
	Account      string
	RestartDelay time.Duration
}

// Host is every call into Windows the feature makes (NFR-003). The real one is
// in host_windows.go; tests use fakehost.
type Host interface {
	// Elevated reports whether the process runs with Administrator rights (FR-005).
	Elevated() bool
	// Executable is the path of the running binary.
	Executable() (string, error)
	// ProgramDir is the per-machine program directory (FR-007).
	ProgramDir() (string, error)
	// ConfigDir is the machine-wide configuration directory (FR-012).
	ConfigDir() (string, error)
	// Service describes the omnistat service; Exists is false when there is none.
	Service(ctx context.Context) (Installed, error)
	// CopyBinary installs src at dst, replacing an existing file (FR-007, FR-017).
	CopyBinary(src, dst string) error
	// RemoveBinary deletes path and its directory if empty (FR-020). When path
	// is the running program it cannot be deleted: it is moved aside and
	// leftover names the moved file, which Windows deletes at the next restart.
	// path itself is never scheduled for deletion, so a reinstall before that
	// restart keeps its binary.
	RemoveBinary(path string) (leftover string, err error)
	// EnsureConfigDir creates path if needed and restricts writing to
	// Administrators and SYSTEM; tightened reports that permissions changed (FR-012).
	EnsureConfigDir(path string) (tightened bool, err error)
	// FileExists reports whether path exists.
	FileExists(path string) bool
	// CreateFile writes data to path unless path exists; created reports
	// whether it did. The file inherits its directory's permissions (007 FR-014).
	CreateFile(path string, data []byte) (created bool, err error)
	// Register creates or updates the service with r (FR-008–FR-010).
	Register(ctx context.Context, r Registration) error
	// StoreEnv replaces the service's stored settings and restricts reading them
	// to Administrators and SYSTEM (FR-013, FR-015).
	StoreEnv(ctx context.Context, env map[string]string) error
	// EventSource registers (true) or removes (false) the event source (FR-026).
	EventSource(install bool) error
	// Start starts the service.
	Start(ctx context.Context) error
	// Stop stops the service and waits until it has stopped (FR-017, FR-020).
	Stop(ctx context.Context) error
	// State reports the service's run state.
	State(ctx context.Context) (State, error)
	// Delete removes the service registration and its stored settings (FR-020).
	Delete(ctx context.Context) error
	// Prompter reads the token without echo (FR-014) and a missing project id
	// (007 FR-017); service.ErrNotInteractive without a console.
	service.Prompter
}
