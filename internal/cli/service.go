package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// serviceSettle is how long install watches the service after starting it (spec 006 FR-011).
const serviceSettle = 5 * time.Second

func (a *App) serviceSettle() time.Duration {
	if a.ServiceSettle > 0 {
		return a.ServiceSettle
	}
	return serviceSettle
}

// service implements `omnistat service install|uninstall` (spec 006).
func (a *App) service(e env, args []string) int {
	host := a.ServiceHost
	if host == nil {
		h, err := winsvc.NewHost()
		if errors.Is(err, winsvc.ErrUnsupported) {
			// FR-027
			fmt.Fprintf(e.stderr, "omnistat: service: not supported on %s — the service commands are Windows-only; elsewhere run `omnistat run --daemon` under systemd or launchd\n", a.goos())
			return ExitError
		}
		if err != nil {
			return fail(e, err)
		}
		host = h
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "install":
		return a.serviceInstall(e, host, args[1:])
	case "uninstall":
		return a.serviceUninstall(e, host, args[1:])
	default:
		fmt.Fprintln(e.stderr, "usage: omnistat service install [--dry-run] [--replace-token] | uninstall [--dry-run]")
		return ExitError
	}
}

func (a *App) serviceInstall(e env, host winsvc.Host, args []string) int {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dryRun := fs.Bool("dry-run", false, "run the checks and print what would change; change nothing")
	replaceToken := fs.Bool("replace-token", false, "ask for a new access token even if one is stored")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	p, err := winsvc.PlanInstall(e.ctx, host, winsvc.InstallOptions{
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
	fmt.Fprintf(e.stdout, "omnistat is installed and running as service %q.\nLogs: Event Viewer → Windows Logs → Application, source %q.\n", winsvc.Name, winsvc.EventSource)
	return ExitOK
}

// printInstallSummary is the US-1/1 report. Settings are named, never shown (FR-016).
func (a *App) printInstallSummary(e env, p *winsvc.Plan) {
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
	fmt.Fprintf(e.stdout, "  binary:   %s\n", p.Binary)
	cfgNote := "not present — defaults apply"
	if _, err := os.Stat(p.ConfigPath); err == nil {
		cfgNote = "present"
	}
	fmt.Fprintf(e.stdout, "  config:   %s (%s)\n", p.ConfigPath, cfgNote)
	fmt.Fprintf(e.stdout, "  settings: %s (stored for the service, values not shown)\n", strings.Join(p.Stored, ", "))
}

// servicePrecheck runs the read-only checks of `omnistat identity` with the
// settings and config file the service will get (spec 006 FR-006). Unlike
// `identity`, it requires the project: the service cannot run without it. A
// project whose schema is not applied yet passes; the service applies it.
func (a *App) servicePrecheck(e env) func(context.Context, func(string) string, string) (winsvc.Checked, error) {
	return func(ctx context.Context, getenv func(string) string, cfgPath string) (winsvc.Checked, error) {
		s, err := config.LoadWithDefault("", cfgPath, getenv)
		if err != nil {
			return winsvc.Checked{}, err
		}
		if err := s.RequireAPI(); err != nil {
			return winsvc.Checked{}, err
		}
		ce := e
		ce.ctx, ce.getenv = ctx, getenv
		rep, err := a.identityCheck(ce, s)
		if err != nil {
			return winsvc.Checked{}, err
		}
		c := winsvc.Checked{Identity: rep.Identity, Source: rep.Source, EntityID: rep.EntityID, WouldCreate: rep.WouldCreate}
		switch {
		case errors.Is(rep.err, errSchemaNotReady):
			c.SchemaPending = true
		case rep.err != nil:
			return winsvc.Checked{}, rep.err
		}
		return c, nil
	}
}

func (a *App) serviceUninstall(e env, host winsvc.Host, args []string) int {
	fs := flag.NewFlagSet("service uninstall", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dryRun := fs.Bool("dry-run", false, "print what would be removed; change nothing")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	p, err := winsvc.PlanUninstall(e.ctx, host)
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
