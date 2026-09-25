package winsvc

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/omnismith-apps/omnistat/internal/config"
)

// Checked is what the install pre-check found (FR-006, US-1/1).
type Checked struct {
	Identity    string
	Source      string
	EntityID    string
	WouldCreate bool
	// SchemaPending: the project has no identity attribute yet; the service
	// reconciles the schema when it starts.
	SchemaPending bool
}

// InstallOptions are the inputs of PlanInstall.
type InstallOptions struct {
	// Getenv is the installer's environment (FR-013).
	Getenv func(string) string
	// ReplaceToken forces the token prompt (FR-018).
	ReplaceToken bool
	// Precheck runs the read-only checks of `omnistat identity` with the
	// settings the service will get (FR-006).
	Precheck func(ctx context.Context, getenv func(string) string, configPath string) (Checked, error)
	// Settle is how long the service is watched after it starts (FR-011);
	// zero checks once. Poll is the interval between checks.
	Settle, Poll time.Duration
}

// PlanInstall decides everything `service install` will do, changing nothing:
// elevation (FR-005), ownership of an existing service (FR-019), the settings
// to store (FR-013, FR-014, FR-017, FR-018) and the pre-check (FR-006) all
// happen here, before the first step is applied.
func PlanInstall(ctx context.Context, h Host, o InstallOptions) (*Plan, error) {
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

	env, err := resolveEnv(h, inst.Env, o)
	if err != nil {
		return nil, err
	}
	p := &Plan{Upgrade: inst.Exists, Binary: target, ConfigPath: winJoin(cfgDir, ConfigFile), Stored: slices.Sorted(maps.Keys(env))}
	if o.Precheck != nil {
		if p.Checked, err = o.Precheck(ctx, func(k string) string { return env[k] }, p.ConfigPath); err != nil {
			return nil, err
		}
	}

	if inst.Exists && inst.State != StateStopped {
		p.add("stop the running service (it publishes what it has buffered first)", func(ctx context.Context) (string, error) {
			return "", h.Stop(ctx)
		})
	}
	if !samePath(exe, target) {
		desc := fmt.Sprintf("install %s as %s", exe, target)
		if inst.Exists {
			desc = fmt.Sprintf("replace %s with %s", target, exe)
		}
		p.add(desc, func(context.Context) (string, error) { return "", h.CopyBinary(exe, target) })
	}
	p.add(fmt.Sprintf("ensure %s exists, writable only by Administrators and SYSTEM", cfgDir), func(context.Context) (string, error) {
		tightened, err := h.EnsureConfigDir(cfgDir)
		return noteIf(tightened, "tightened its permissions: other users could write to it"), err
	})
	reg := Registration{Binary: target, Args: Args, DisplayName: DisplayName, Description: Description, Account: Account, RestartDelay: RestartDelay}
	verb := "register"
	if inst.Exists {
		verb = "update"
	}
	p.add(fmt.Sprintf("%s service %s: \"%s\" %s, automatic start (delayed), account %s, restart %s after any failure",
		verb, Name, target, strings.Join(Args, " "), Account, RestartDelay), func(ctx context.Context) (string, error) {
		return "", h.Register(ctx, reg)
	})
	p.add("store the service's settings, readable only by Administrators and SYSTEM: "+strings.Join(p.Stored, ", "), func(ctx context.Context) (string, error) {
		return "", h.StoreEnv(ctx, env)
	})
	p.add("register event source "+EventSource+" in the Application log", func(context.Context) (string, error) {
		return "", h.EventSource(true)
	})
	p.add("start the service and check that it keeps running", func(ctx context.Context) (string, error) {
		if err := h.Start(ctx); err != nil {
			return "", err
		}
		return watch(ctx, h, o.Settle, o.Poll)
	})
	return p, nil
}

// resolveEnv merges the stored settings with those set in the installer's
// environment (FR-013, FR-017) and settles the token (FR-014, FR-018).
func resolveEnv(h Host, stored map[string]string, o InstallOptions) (map[string]string, error) {
	env := maps.Clone(stored)
	if env == nil {
		env = map[string]string{}
	}
	getenv := o.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	for _, k := range Captured {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			env[k] = v
		}
	}
	fromEnv := strings.TrimSpace(getenv(config.EnvToken)) != ""
	if !fromEnv && (o.ReplaceToken || env[config.EnvToken] == "") {
		tok, err := h.PromptSecret("Omnismith access token (input hidden): ")
		switch {
		case errors.Is(err, ErrNotInteractive):
			return nil, ErrNoToken
		case err != nil:
			return nil, fmt.Errorf("reading the token: %w", err)
		}
		if tok = strings.TrimSpace(tok); tok == "" {
			return nil, ErrNoToken
		}
		env[config.EnvToken] = tok
	}
	return env, nil
}

// watch fails when the service stops within settle of starting (FR-011).
func watch(ctx context.Context, h Host, settle, poll time.Duration) (string, error) {
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := time.Now().Add(settle)
	for {
		st, err := h.State(ctx)
		if err != nil {
			return "", err
		}
		if st == StateStopped {
			return "", ErrStartFailed
		}
		if !time.Now().Before(deadline) {
			return "service is " + st.String(), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}
}
