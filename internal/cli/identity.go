package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

// identityReport is the JSON shape of `omnistat identity --json`.
type identityReport struct {
	Identity    string   `json:"identity"`
	Source      string   `json:"source"`
	Project     bool     `json:"project_checked"`
	SchemaOK    bool     `json:"schema_ok"`
	EntityID    string   `json:"entity_id,omitempty"`
	WouldCreate bool     `json:"would_create"`
	Duplicates  []string `json:"duplicates,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// identity implements `omnistat identity` (spec 002 FR-017): the identity
// value and source always; the resolution result when the project is
// reachable. It never writes.
func (a *App) identity(e env, args []string) int {
	fs := flag.NewFlagSet("identity", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	asJSON := fs.Bool("json", false, "emit the report as JSON")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	s, err := config.Load(e.config, e.getenv)
	if err != nil {
		return fail(e, err)
	}
	mod, ok := a.Registry.Get(machineid.Name)
	provider, isProvider := mod.(*machineid.Module)
	if !ok || !isProvider {
		return fail(e, errors.New("machine-id module is not available in this build"))
	}
	id, err := provider.Discover(e.ctx, s.Identity)
	if err != nil {
		return fail(e, err)
	}
	rep := identityReport{Identity: id.Value, Source: id.Source}

	// Resolution needs the project; without credentials report the identity only.
	if s.RequireAPI() == nil {
		rep.Project = true
		if err := a.resolveDryRun(e, s, id, &rep); err != nil {
			rep.Error = err.Error()
		}
	}
	return a.printIdentity(e, rep, *asJSON)
}

func (a *App) resolveDryRun(e env, s config.Settings, id machineid.Identity, rep *identityReport) error {
	p, err := a.prepareWith(e, s)
	if err != nil {
		return err
	}
	if plan := schema.Diff(p.desired, p.current); len(plan.Conflicts) > 0 {
		return fmt.Errorf("schema is not ready for identity: %w", &schema.ConflictError{Conflicts: plan.Conflicts})
	}
	target, err := identityTarget(p.desired, schema.ResolvedFrom(p.current))
	if err != nil {
		return err
	}
	rep.SchemaOK = true
	h, err := identity.Resolve(e.ctx, p.api, target, id.Value, true, e.log)
	if err != nil {
		return explain(err)
	}
	rep.EntityID, rep.WouldCreate, rep.Duplicates = h.EntityID, h.WouldCreate, h.Duplicates
	return nil
}

// identityTarget locates the (possibly remapped) identity attribute and its
// template among the resolved ids (spec 002 FR-010, spec 003 FR-022). It
// fails when reconciliation has not happened yet.
func identityTarget(desired manifest.Desired, resolved schema.Resolved) (identity.Target, error) {
	attr, ok := desired.Find(machineid.Name, machineid.AttributeKey)
	if !ok {
		return identity.Target{}, errors.New("machine-id module declares no identity attribute (bug)")
	}
	tplSlug := attr.Templates[0]
	tplID, tplOK := resolved.Templates[tplSlug]
	_, attrOK := resolved.Attributes[attr.Slug]
	if !tplOK || !attrOK {
		return identity.Target{}, fmt.Errorf("schema is not ready for identity (template %q / attribute %q): run `omnistat schema plan`", tplSlug, attr.Slug)
	}
	return identity.Target{TemplateSlug: tplSlug, TemplateID: tplID, AttributeSlug: attr.Slug}, nil
}

func (a *App) printIdentity(e env, rep identityReport, asJSON bool) int {
	if asJSON {
		out, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return fail(e, err)
		}
		fmt.Fprintln(e.stdout, string(out))
	} else {
		fmt.Fprintf(e.stdout, "identity: %s\nsource:   %s\n", rep.Identity, rep.Source)
		switch {
		case !rep.Project:
			fmt.Fprintf(e.stdout, "entity:   (not checked — set %s and %s to resolve)\n", config.EnvToken, config.EnvProjectID)
		case rep.Error != "":
			fmt.Fprintf(e.stdout, "entity:   (unresolved)\n")
		case rep.WouldCreate:
			fmt.Fprintf(e.stdout, "entity:   none yet — the first run will create one\n")
		default:
			fmt.Fprintf(e.stdout, "entity:   %s\n", rep.EntityID)
			if len(rep.Duplicates) > 1 {
				fmt.Fprintf(e.stdout, "warning:  %d entities carry this identity: %v (using the oldest)\n", len(rep.Duplicates), rep.Duplicates)
			}
		}
	}
	if rep.Error != "" {
		fmt.Fprintf(e.stderr, "omnistat: %s\n", rep.Error)
		return ExitError
	}
	return ExitOK
}
