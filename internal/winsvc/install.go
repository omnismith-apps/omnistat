package winsvc

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/service"
)

// LogHint is where the service logs, as install reports it (FR-025).
const LogHint = `Event Viewer → Windows Logs → Application, source "` + EventSource + `"`

// PlanInstall decides everything `service install` will do, changing nothing:
// elevation (FR-005), ownership of an existing service (FR-019), the settings
// to store (FR-013, FR-014, FR-017, FR-018; the project-id prompt of 007
// FR-017) and the pre-check (FR-006) all
// happen here, before the first step is applied.
func PlanInstall(ctx context.Context, h Host, o service.InstallOptions) (*service.Plan, error) {
	if !h.Elevated() {
		return nil, ErrNotElevated
	}
	exe, err := h.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating the running binary: %w", err)
	}
	progDir, err := h.ProgramDir()
	if err != nil {
		return nil, fmt.Errorf("locating the program directory: %w", err)
	}
	cfgDir, err := h.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locating the configuration directory: %w", err)
	}
	target := winJoin(progDir, BinaryName)
	inst, err := h.Service(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying the service manager: %w", err)
	}
	if err := ours(inst, target); err != nil {
		return nil, err
	}

	cfgPath := winJoin(cfgDir, ConfigFile)
	env, err := service.ResolveSettings(service.SettingsOptions{
		Stored: inst.Env, Getenv: o.Getenv, Names: service.Captured, ReplaceToken: o.ReplaceToken, ConfigPath: cfgPath, Prompt: h,
	})
	if err != nil {
		return nil, err
	}
	p := &service.Plan{Upgrade: inst.Exists, Binary: target, ConfigPath: cfgPath, LogHint: LogHint, Stored: slices.Sorted(maps.Keys(env))}
	if o.Precheck != nil {
		if p.Checked, err = o.Precheck(ctx, func(k string) string { return env[k] }, p.ConfigPath); err != nil {
			return nil, err
		}
	}

	if inst.Exists && inst.State != StateStopped {
		p.Add("stop the running service (it publishes what it has buffered first)", func(ctx context.Context) (string, error) {
			return "", h.Stop(ctx)
		})
	}
	if !samePath(exe, target) {
		desc := fmt.Sprintf("install %s as %s", exe, target)
		if inst.Exists {
			desc = fmt.Sprintf("replace %s with %s", target, exe)
		}
		p.Add(desc, func(context.Context) (string, error) { return "", h.CopyBinary(exe, target) })
	}
	p.Add(fmt.Sprintf("ensure %s exists, writable only by Administrators and SYSTEM", cfgDir), func(context.Context) (string, error) {
		tightened, err := h.EnsureConfigDir(cfgDir)
		return service.NoteIf(tightened, "tightened its permissions: other users could write to it"), err
	})
	if p.ConfigPresent = h.FileExists(cfgPath); !p.ConfigPresent {
		p.Add("create "+cfgPath+": a commented starter configuration (every default stays in force)", func(context.Context) (string, error) {
			created, err := h.CreateFile(cfgPath, config.Starter)
			return service.NoteIf(!created, "it appeared meanwhile and was left as it is"), err
		})
	}
	reg := Registration{Binary: target, Args: Args, DisplayName: DisplayName, Description: Description, Account: Account, RestartDelay: RestartDelay}
	verb := "register"
	if inst.Exists {
		verb = "update"
	}
	p.Add(fmt.Sprintf("%s service %s: \"%s\" %s, automatic start (delayed), account %s, restart %s after any failure",
		verb, Name, target, strings.Join(Args, " "), Account, RestartDelay), func(ctx context.Context) (string, error) {
		return "", h.Register(ctx, reg)
	})
	p.Add("store the service's settings, readable only by Administrators and SYSTEM: "+strings.Join(p.Stored, ", "), func(ctx context.Context) (string, error) {
		return "", h.StoreEnv(ctx, env)
	})
	p.Add("register event source "+EventSource+" in the Application log", func(context.Context) (string, error) {
		return "", h.EventSource(true)
	})
	p.Add("start the service and check that it keeps running", func(ctx context.Context) (string, error) {
		if err := h.Start(ctx); err != nil {
			return "", err
		}
		return service.Watch(ctx, o.Settle, o.Poll, func(ctx context.Context) (string, error) {
			st, err := h.State(ctx)
			if err == nil && st == StateStopped {
				err = ErrStartFailed
			}
			return st.String(), err
		})
	})
	return p, nil
}
