package systemd

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/service"
)

// File modes install sets (spec 007 "Data & integration contract").
const (
	modeBinary fs.FileMode = 0o755
	modeDir    fs.FileMode = 0o755
	modeConfig fs.FileMode = 0o644
	modeEnv    fs.FileMode = 0o600
	modeUnit   fs.FileMode = 0o644
)

// Captured is what install stores for the service: the settings of every
// platform, plus the lower-case proxy spellings Linux tools use (FR-013).
var Captured = append(slices.Clone(service.Captured), "https_proxy", "http_proxy", "no_proxy")

// PlanInstall decides everything `service install` will do, changing nothing:
// root and systemd (FR-002, FR-004), ownership of an existing unit (FR-019),
// the settings to store (FR-013, FR-015, FR-017, FR-018) and the pre-check
// (FR-005) all happen here, before the first step is applied.
func PlanInstall(ctx context.Context, h Host, o service.InstallOptions) (*service.Plan, error) {
	if err := preconditions(h); err != nil {
		return nil, err
	}
	exe, err := h.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating the running binary: %w", err)
	}
	unit, installed, err := inspect(ctx, h)
	if err != nil {
		return nil, err
	}
	stored, err := readStored(h)
	if err != nil {
		return nil, err
	}
	env, err := service.ResolveSettings(service.SettingsOptions{
		Stored: stored, Getenv: o.Getenv, Names: Captured, ReplaceToken: o.ReplaceToken, ConfigPath: ConfigPath, Prompt: h,
	})
	if err != nil {
		return nil, err
	}
	envData, err := FormatEnv(env)
	if err != nil {
		return nil, err
	}
	p := &service.Plan{Upgrade: installed, Binary: BinaryPath, ConfigPath: ConfigPath, Unit: UnitPath, LogHint: LogHint, Stored: slices.Sorted(maps.Keys(env))}
	if o.Precheck != nil {
		if p.Checked, err = o.Precheck(ctx, func(k string) string { return env[k] }, ConfigPath); err != nil {
			return nil, err
		}
	}

	if installed && unit.Active() {
		p.Add("stop the running service (it publishes what it has buffered first)", func(ctx context.Context) (string, error) {
			return "", h.Systemctl(ctx, "stop", UnitName)
		})
	}
	if exe != BinaryPath {
		if err := planBinary(h, p, exe); err != nil {
			return nil, err
		}
	}
	if err := planConfig(h, p); err != nil {
		return nil, err
	}
	p.Add(fmt.Sprintf("write %s (root, %04o, readable by root only): %s", EnvPath, modeEnv, strings.Join(p.Stored, ", ")), func(context.Context) (string, error) {
		return "", h.WriteFile(EnvPath, envData, modeEnv)
	})
	p.AddDetailed(fmt.Sprintf("write %s (root, %04o):", UnitPath, modeUnit), UnitText(), func(context.Context) (string, error) {
		return "", h.WriteFile(UnitPath, []byte(UnitText()), modeUnit)
	})
	p.Add("systemctl daemon-reload (load the unit)", func(ctx context.Context) (string, error) {
		return "", h.Systemctl(ctx, "daemon-reload")
	})
	p.Add("systemctl enable "+UnitName+" (start at boot)", func(ctx context.Context) (string, error) {
		return "", h.Systemctl(ctx, "enable", UnitName)
	})
	p.Add("start the service and check that it keeps running", func(ctx context.Context) (string, error) {
		if err := h.Systemctl(ctx, "start", UnitName); err != nil {
			return "", fmt.Errorf("%w (%w)", ErrStartFailed, err)
		}
		return service.Watch(ctx, o.Settle, o.Poll, func(ctx context.Context) (string, error) {
			u, err := h.Unit(ctx)
			switch {
			case err != nil:
				return "", err
			case u.ActiveState == "failed", u.ActiveState == "inactive", u.SubState == "auto-restart":
				return "", fmt.Errorf("%w (it is %s/%s)", ErrStartFailed, u.ActiveState, u.SubState)
			}
			return fmt.Sprintf("%s (%s)", u.ActiveState, u.SubState), nil
		})
	})
	for _, d := range unit.DropInPaths {
		p.Keep = append(p.Keep, d+" (your drop-in)")
	}
	return p, nil
}

func preconditions(h Host) error {
	if !h.Root() {
		return ErrNotRoot
	}
	if !h.Booted() {
		return ErrNotBooted
	}
	return nil
}

// inspect reads the unit's state and whether it is omnistat's own (FR-019):
// ours is /etc/systemd/system/omnistat.service beginning with the marker, and
// systemd loads it from there (or has not loaded it yet).
func inspect(ctx context.Context, h Host) (Unit, bool, error) {
	u, err := h.Unit(ctx)
	if err != nil {
		return Unit{}, false, fmt.Errorf("querying systemd: %w", err)
	}
	if u.LoadState == "masked" {
		return Unit{}, false, fmt.Errorf("%w: it is masked (systemctl unmask %s); nothing was changed", ErrForeignUnit, UnitName)
	}
	f, err := h.Stat(UnitPath)
	if err != nil {
		return Unit{}, false, err
	}
	if f.Exists {
		data, err := h.ReadFile(UnitPath)
		if err != nil {
			return Unit{}, false, err
		}
		if !OwnUnit(data) {
			return Unit{}, false, fmt.Errorf("%w: %s; nothing was changed", ErrForeignUnit, UnitPath)
		}
	}
	if u.FragmentPath != "" && u.FragmentPath != UnitPath && u.LoadState != "not-found" {
		return Unit{}, false, fmt.Errorf("%w: %s; nothing was changed", ErrForeignUnit, u.FragmentPath)
	}
	return u, f.Exists, nil
}

// readStored reads the settings the service has now. The error names the file
// and line, never the content (spec edge case).
func readStored(h Host) (map[string]string, error) {
	f, err := h.Stat(EnvPath)
	if err != nil || !f.Exists {
		return nil, err
	}
	data, err := h.ReadFile(EnvPath)
	if err != nil {
		return nil, err
	}
	env, err := ParseEnv(data)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w (fix or remove that line, then run install again)", EnvPath, err)
	}
	return env, nil
}

// planBinary installs the running binary at BinaryPath (FR-006).
func planBinary(h Host, p *service.Plan, exe string) error {
	f, err := h.Stat(BinaryPath)
	if err != nil {
		return err
	}
	desc := fmt.Sprintf("install %s as %s (root, %04o)", exe, BinaryPath, modeBinary)
	if f.Exists {
		desc = fmt.Sprintf("replace %s with %s (root, %04o)", BinaryPath, exe, modeBinary)
	}
	p.Add(desc, func(context.Context) (string, error) {
		data, err := h.ReadFile(exe)
		if err != nil {
			return "", err
		}
		if err := h.MkdirAll(filepath.Dir(BinaryPath), modeDir); err != nil {
			return "", err
		}
		return "", h.WriteFile(BinaryPath, data, modeBinary)
	})
	return nil
}

// planConfig creates the config directory and the starter file (FR-012,
// FR-014), or fixes what others could change or the service could not read.
// An existing config file is never rewritten.
func planConfig(h Host, p *service.Plan) error {
	dir, err := h.Stat(ConfigDir)
	if err != nil {
		return err
	}
	switch {
	case !dir.Exists:
		p.Add(fmt.Sprintf("create %s (root, %04o)", ConfigDir, modeDir), func(context.Context) (string, error) {
			return "", h.MkdirAll(ConfigDir, modeDir)
		})
	case !dir.Dir:
		return fmt.Errorf("%s exists and is not a directory", ConfigDir)
	case loose(dir):
		addFix(h, p, ConfigDir, dir, modeDir)
	}
	cfg, err := h.Stat(ConfigPath)
	if err != nil {
		return err
	}
	p.ConfigPresent = cfg.Exists
	switch {
	case !cfg.Exists:
		p.Add(fmt.Sprintf("create %s (root, %04o): a commented starter configuration; every default stays in force", ConfigPath, modeConfig), func(context.Context) (string, error) {
			created, err := h.CreateFile(ConfigPath, config.Starter, modeConfig)
			return service.NoteIf(!created, "it appeared meanwhile and was left as it is"), err
		})
	case loose(cfg):
		addFix(h, p, ConfigPath, cfg, modeConfig)
	}
	return nil
}

// loose reports a config path the service must not trust or cannot read
// (FR-012): not owned by root, writable by group or others, or not readable
// (a directory: not searchable) by others, which the dynamic user is.
func loose(f File) bool {
	need := fs.FileMode(0o004)
	if f.Dir {
		need = 0o005
	}
	return f.UID != 0 || f.Mode&0o022 != 0 || f.Mode&need != need
}

func addFix(h Host, p *service.Plan, path string, f File, mode fs.FileMode) {
	p.Add(fmt.Sprintf("fix %s: owner uid %d, mode %04o → root, %04o (only root may change what the service reads, and the service must read it)",
		path, f.UID, f.Mode, mode), func(context.Context) (string, error) {
		return "", h.Secure(path, mode)
	})
}
