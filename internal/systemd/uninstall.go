package systemd

import (
	"context"

	"github.com/omnismith-apps/omnistat/internal/service"
)

// PlanUninstall decides everything `service uninstall` will do (FR-020),
// changing nothing. The settings file (with the token) and the binary go
// before the unit, so an interrupted uninstall still finds the unit and is
// finished by running it again. The configuration directory and the admin's
// drop-ins are kept (FR-021); the Omnismith project is never touched.
func PlanUninstall(ctx context.Context, h Host) (*service.Plan, error) {
	if err := preconditions(h); err != nil {
		return nil, err
	}
	unit, installed, err := inspect(ctx, h)
	if err != nil {
		return nil, err
	}
	if !installed {
		return &service.Plan{NotInstalled: true}, nil
	}

	p := &service.Plan{Binary: BinaryPath, ConfigPath: ConfigPath, Unit: UnitPath, LogHint: LogHint}
	if unit.Active() {
		p.Add("stop the service (it publishes what it has buffered first)", func(ctx context.Context) (string, error) {
			return "", h.Systemctl(ctx, "stop", UnitName)
		})
	}
	p.Add("systemctl disable "+UnitName+" (no start at boot)", func(ctx context.Context) (string, error) {
		return "", h.Systemctl(ctx, "disable", UnitName)
	})
	p.Add("systemctl reset-failed "+UnitName+" (forget past failures)", func(ctx context.Context) (string, error) {
		if err := h.Systemctl(ctx, "reset-failed", UnitName); err != nil {
			return "nothing to reset", nil // a unit that never failed has nothing to reset
		}
		return "", nil
	})
	for _, path := range []string{EnvPath, BinaryPath, UnitPath} {
		desc := "remove " + path
		if path == EnvPath {
			desc += " (the stored settings, with the access token)"
		}
		p.Add(desc, func(context.Context) (string, error) { return "", h.Remove(path) })
	}
	p.Add("systemctl daemon-reload (forget the unit)", func(ctx context.Context) (string, error) {
		return "", h.Systemctl(ctx, "daemon-reload")
	})

	keep := ConfigDir + " (your configuration directory)"
	if f, err := h.Stat(ConfigPath); err == nil && f.Exists {
		keep = ConfigDir + " with omnistat.yaml (your configuration)"
	}
	p.Keep = append(p.Keep, keep)
	for _, d := range unit.DropInPaths {
		p.Keep = append(p.Keep, d+" (your drop-in)")
	}
	return p, nil
}
