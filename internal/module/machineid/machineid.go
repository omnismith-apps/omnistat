// Package machineid is the identity module (spec 002): it declares the
// `machine_id` attribute and provides a stable identity for the host,
// autodiscovered from the operating system or pinned by the operator.
package machineid

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// Name is the module name.
const Name = "machine-id"

// Identity sources (FR-017).
const (
	SourceStatic  = "static"
	SourceLinux   = "linux-machine-id"
	SourceDarwin  = "darwin-platform-uuid"
	SourceWindows = "windows-machine-guid" // spec 006 FR-003
)

// MaxStaticLen bounds a static identity (FR-008).
const MaxStaticLen = 128

// derivationKey is the application-specific key of the identity derivation
// (FR-007). Changing it changes every host's identity: ADR-0004.
const derivationKey = "omnistat/host-identity/v1"

// ErrNoIdentity is returned when neither a static identity nor an OS id is
// available (FR-006). omnistat never invents one.
var ErrNoIdentity = errors.New("no host identity")

// Identity is the value omnistat publishes and where it came from.
type Identity struct {
	Value  string
	Source string
}

// Module is the machine-id module. The zero value is not usable; call New.
// FS, IOReg, MachineGUID and GOOS are injectable for tests.
type Module struct {
	// FS is the root file system (paths without leading slash).
	FS fs.FS
	// IOReg returns the output of `ioreg -rd1 -c IOPlatformExpertDevice` (macOS).
	IOReg func(ctx context.Context) ([]byte, error)
	// MachineGUID returns the machine GUID Windows keeps for the OS
	// installation (spec 006 FR-002).
	MachineGUID func() (string, error)
	// GOOS selects the discovery strategy.
	GOOS string
}

// New returns the module bound to the real OS.
func New() *Module {
	return &Module{
		FS: os.DirFS("/"),
		IOReg: func(ctx context.Context) ([]byte, error) {
			return exec.CommandContext(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		},
		MachineGUID: readMachineGUID,
		GOOS:        runtime.GOOS,
	}
}

// Name implements module.Module.
func (m *Module) Name() string { return Name }

// Manifest implements module.Module (FR-001).
func (m *Module) Manifest() manifest.Manifest {
	return manifest.Manifest{
		Module: Name,
		Attributes: []manifest.Attribute{{
			Key:         "id",
			Name:        "Machine ID",
			Slug:        "machine_id",
			Kind:        manifest.KindText,
			Description: "Stable identity of the host reporting through omnistat (derived from the OS machine id, or set by the operator)",
		}},
	}
}

// AttributeKey is the manifest key of the identity attribute.
const AttributeKey = "id"

// ValidateStatic checks an operator-supplied identity (FR-008).
func ValidateStatic(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("static identity is empty")
	}
	if len(s) > MaxStaticLen {
		return fmt.Errorf("static identity is %d characters, maximum is %d", len(s), MaxStaticLen)
	}
	return nil
}

// Derive turns a raw OS machine id into the published identity: a keyed
// one-way hash rendered as 64 lowercase hex characters (FR-007).
func Derive(raw string) string {
	mac := hmac.New(sha256.New, []byte(derivationKey))
	mac.Write([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Discover returns the identity: the static value verbatim when given
// (FR-005/008), otherwise the derived OS id (FR-004/007), otherwise
// ErrNoIdentity with guidance (FR-006). The raw OS id never leaves this
// function except as Derive's input (NFR-004).
func (m *Module) Discover(ctx context.Context, static string) (Identity, error) {
	if strings.TrimSpace(static) != "" {
		if err := ValidateStatic(static); err != nil {
			return Identity{}, err
		}
		return Identity{Value: strings.TrimSpace(static), Source: SourceStatic}, nil
	}
	var raw, source string
	var looked []string
	switch m.GOOS {
	case "linux":
		source = SourceLinux
		for _, path := range []string{"etc/machine-id", "var/lib/dbus/machine-id"} {
			looked = append(looked, "/"+path)
			if v := m.readFile(path); v != "" {
				raw = v
				break
			}
		}
	case "darwin":
		source = SourceDarwin
		looked = append(looked, "ioreg IOPlatformUUID")
		raw = m.platformUUID(ctx)
	case "windows":
		source = SourceWindows
		looked = append(looked, machineGUIDLocation)
		raw = m.machineGUID()
	default:
		return Identity{}, fmt.Errorf("%w: autodiscovery is not supported on %s; set identity.static or OMNISTAT_IDENTITY", ErrNoIdentity, m.GOOS)
	}
	if raw == "" {
		return Identity{}, fmt.Errorf("%w: looked at %s; set identity.static in omnistat.yaml or OMNISTAT_IDENTITY", ErrNoIdentity, strings.Join(looked, ", "))
	}
	return Identity{Value: Derive(raw), Source: source}, nil
}

func (m *Module) readFile(path string) string {
	data, err := fs.ReadFile(m.FS, path)
	if err != nil {
		return ""
	}
	return present(string(data))
}

func (m *Module) platformUUID(ctx context.Context) string {
	out, err := m.IOReg(ctx)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		_, after, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		return present(strings.Trim(strings.TrimSpace(after), `"`))
	}
	return ""
}

func (m *Module) machineGUID() string {
	v, err := m.MachineGUID()
	if err != nil {
		return ""
	}
	return present(v)
}

// present trims and rejects empty or all-zero ids (FR-004).
func present(v string) string {
	v = strings.TrimSpace(v)
	if strings.Trim(v, "0-") == "" {
		return ""
	}
	return v
}
