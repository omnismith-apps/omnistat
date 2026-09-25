package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/service"
	"github.com/omnismith-apps/omnistat/internal/systemd"
	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// serviceSettle is how long install watches the service after starting it
// (spec 006 FR-011, 007 FR-011).
const serviceSettle = 5 * time.Second

func (a *App) serviceSettle() time.Duration {
	if a.ServiceSettle > 0 {
		return a.ServiceSettle
	}
	return serviceSettle
}

// installer is one platform's `service install|uninstall`: the Windows
// service manager (spec 006) or systemd (spec 007).
type installer interface {
	PlanInstall(ctx context.Context, o service.InstallOptions) (*service.Plan, error)
	PlanUninstall(ctx context.Context) (*service.Plan, error)
}

type windowsInstaller struct{ h winsvc.Host }

func (w windowsInstaller) PlanInstall(ctx context.Context, o service.InstallOptions) (*service.Plan, error) {
	return winsvc.PlanInstall(ctx, w.h, o)
}

func (w windowsInstaller) PlanUninstall(ctx context.Context) (*service.Plan, error) {
	return winsvc.PlanUninstall(ctx, w.h)
}

type systemdInstaller struct{ h systemd.Host }

func (s systemdInstaller) PlanInstall(ctx context.Context, o service.InstallOptions) (*service.Plan, error) {
	return systemd.PlanInstall(ctx, s.h, o)
}

func (s systemdInstaller) PlanUninstall(ctx context.Context) (*service.Plan, error) {
	return systemd.PlanUninstall(ctx, s.h)
}

// errServiceUnsupported: no service backend on this platform (006 FR-027,
// 007 FR-003).
var errServiceUnsupported = errors.New("not supported on this platform")

// installer picks the backend: an injected host (tests), else the platform's.
func (a *App) installer() (installer, error) {
	switch {
	case a.ServiceHost != nil:
		return windowsInstaller{a.ServiceHost}, nil
	case a.SystemdHost != nil:
		return systemdInstaller{a.SystemdHost}, nil
	}
	switch a.goos() {
	case "windows":
		h, err := winsvc.NewHost()
		if errors.Is(err, winsvc.ErrUnsupported) {
			return nil, errServiceUnsupported
		}
		return windowsInstaller{h}, err
	case "linux":
		h, err := systemd.NewHost()
		if errors.Is(err, systemd.ErrUnsupported) {
			return nil, errServiceUnsupported
		}
		return systemdInstaller{h}, err
	}
	return nil, errServiceUnsupported
}

// service implements `omnistat service install|uninstall` (specs 006, 007).
func (a *App) service(e env, args []string) int {
	inst, err := a.installer()
	if errors.Is(err, errServiceUnsupported) {
		// 006 FR-027, 007 FR-003
		fmt.Fprintf(e.stderr, "omnistat: service: not supported on %s — the service commands support Windows, and Linux with systemd; elsewhere run `omnistat run --daemon` under your init system or supervisor\n", a.goos())
		return ExitError
	}
	if err != nil {
		return fail(e, err)
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "install":
		return a.serviceInstall(e, inst, args[1:])
	case "uninstall":
		return a.serviceUninstall(e, inst, args[1:])
	default:
		fmt.Fprintln(e.stderr, "usage: omnistat service install [--dry-run] [--replace-token] | uninstall [--dry-run]")
		return ExitError
	}
}

func (a *App) serviceInstall(e env, inst installer, args []string) int {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dryRun := fs.Bool("dry-run", false, "run the checks and print what would change; change nothing")
	replaceToken := fs.Bool("replace-token", false, "ask for a new access token even if one is stored")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	p, err := inst.PlanInstall(e.ctx, service.InstallOptions{
		Getenv:       e.getenv,
		ReplaceToken: *replaceToken,
		Precheck:     a.servicePrecheck(e),
		Settle:       a.serviceSettle(),
	})
	if err != nil {
		return fail(e, err)
	}

	title := "omnistat service install"
	if p.Upgrade {
		title += " (update of the installed service)"
	}
	if *dryRun {
		title += " — dry run, nothing is changed"
	}
	fmt.Fprintln(e.stdout, title)
	a.printInstallSummary(e, p)
	if *dryRun {
		p.Describe(e.stdout)
		return ExitOK
	}
	if err := p.Apply(e.ctx, e.stdout); err != nil {
		return fail(e, err)
	}
	fmt.Fprintf(e.stdout, "omnistat is installed and running as service %q.\nLogs: %s\n", "omnistat", p.LogHint)
	return ExitOK
}

// printInstallSummary is the US-1/1 report. Settings are named, never shown
// (006 FR-016, 007 FR-016).
func (a *App) printInstallSummary(e env, p *service.Plan) {
	c := p.Checked
	fmt.Fprintf(e.stdout, "  identity: %s (%s)\n", c.Identity, c.Source)
	switch {
	case c.SchemaPending:
		fmt.Fprintln(e.stdout, "  entity:   the project has no omnistat schema yet — the service applies it when it starts")
	case c.WouldCreate:
		fmt.Fprintln(e.stdout, "  entity:   none yet — the service creates it when it starts")
	default:
		fmt.Fprintf(e.stdout, "  entity:   %s\n", c.EntityID)
	}
	if p.Unit != "" {
		fmt.Fprintf(e.stdout, "  unit:     %s\n", p.Unit)
	}
	fmt.Fprintf(e.stdout, "  binary:   %s\n", p.Binary)
	cfgNote := "not present — install creates a commented starter; defaults apply"
	if p.ConfigPresent {
		cfgNote = "present, kept as it is"
	}
	fmt.Fprintf(e.stdout, "  config:   %s (%s)\n", p.ConfigPath, cfgNote)
	fmt.Fprintf(e.stdout, "  settings: %s (stored for the service, values not shown)\n", strings.Join(p.Stored, ", "))
}

// servicePrecheck runs the read-only checks of `omnistat identity` with the
// settings and config file the service will get (006 FR-006, 007 FR-005).
// Unlike `identity`, it requires the project: the service cannot run without
// it. A project whose schema is not applied yet passes; the service applies it.
func (a *App) servicePrecheck(e env) service.Precheck {
	return func(ctx context.Context, getenv func(string) string, cfgPath string) (service.Checked, error) {
		s, err := config.LoadWithDefault("", cfgPath, getenv)
		if err != nil {
			return service.Checked{}, err
		}
		if err := s.RequireAPI(); err != nil {
			return service.Checked{}, err
		}
		ce := e
		ce.ctx, ce.getenv = ctx, getenv
		rep, err := a.identityCheck(ce, s)
		if err != nil {
			return service.Checked{}, err
		}
		c := service.Checked{Identity: rep.Identity, Source: rep.Source, EntityID: rep.EntityID, WouldCreate: rep.WouldCreate}
		switch {
		case errors.Is(rep.err, errSchemaNotReady):
			c.SchemaPending = true
		case rep.err != nil:
			return service.Checked{}, rep.err
		}
		return c, nil
	}
}

func (a *App) serviceUninstall(e env, inst installer, args []string) int {
	fs := flag.NewFlagSet("service uninstall", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dryRun := fs.Bool("dry-run", false, "print what would be removed; change nothing")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	p, err := inst.PlanUninstall(e.ctx)
	if err != nil {
		return fail(e, err)
	}
	if p.NotInstalled {
		fmt.Fprintf(e.stdout, "omnistat service is not installed; nothing to do.\n")
		return ExitOK
	}
	if *dryRun {
		fmt.Fprintln(e.stdout, "omnistat service uninstall — dry run, nothing is changed")
		p.Describe(e.stdout)
		return ExitOK
	}
	fmt.Fprintln(e.stdout, "omnistat service uninstall")
	if err := p.Apply(e.ctx, e.stdout); err != nil {
		return fail(e, err)
	}
	fmt.Fprintln(e.stdout, "omnistat is uninstalled. Nothing was changed in the Omnismith project; the host entity remains.")
	return ExitOK
}
