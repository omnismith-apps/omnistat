package cli

import (
	"errors"
	"flag"
	"fmt"

	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

// prepared is everything the schema commands share: settings, the desired
// schema, a client and the current schema.
type prepared struct {
	settings config.Settings
	desired  manifest.Desired
	api      *omni.Client
	current  schema.Current
}

// prepare runs steps 1–4 of the data flow: config → modules → manifests →
// desired; client; one schema read (NFR-002).
func (a *App) prepare(e env) (prepared, error) {
	var p prepared
	s, err := config.Load(e.config, e.getenv)
	if err != nil {
		return p, err
	}
	if err := s.RequireAPI(); err != nil {
		return p, err
	}
	mods, err := a.Registry.Enabled(s.Modules)
	if err != nil {
		return p, err
	}
	manifests := module.Manifests(mods)
	if err := manifest.Validate(manifests); err != nil {
		return p, fmt.Errorf("invalid module manifests (bug in this build): %w", err)
	}
	desired, err := manifest.Resolve(manifests, s.Overrides)
	if err != nil {
		return p, fmt.Errorf("config: %w", err)
	}
	api, err := omni.New(omni.Settings{
		BaseURL: s.BaseURL, Token: s.Token, ProjectID: s.ProjectID,
		Timeout: s.HTTP.Timeout, Retries: s.HTTP.Retries, Version: a.Version, Logger: e.log,
	})
	if err != nil {
		return p, err
	}
	cur, err := api.ReadSchema(e.ctx)
	if err != nil {
		return p, explain(err)
	}
	return prepared{settings: s, desired: desired, api: api, current: cur}, nil
}

// explain turns well-known API errors into operator guidance.
func explain(err error) error {
	switch {
	case errors.Is(err, omni.ErrUnauthorized):
		return fmt.Errorf("%w (check %s): %w", omni.ErrUnauthorized, config.EnvToken, err)
	case errors.Is(err, omni.ErrNoProject):
		return fmt.Errorf("%w (check %s or project_id in the config file): %w", omni.ErrNoProject, config.EnvProjectID, err)
	case errors.Is(err, omni.ErrProjectDeny):
		return fmt.Errorf("%w (check %s or project_id in the config file): %w", omni.ErrProjectDeny, config.EnvProjectID, err)
	case errors.Is(err, omni.ErrStaleGrant):
		return fmt.Errorf("%w — create a new access token for this project: %w", omni.ErrStaleGrant, err)
	case errors.Is(err, omni.ErrForbidden):
		return fmt.Errorf("the token cannot write the schema — ask a project admin to run `omnistat schema apply` once, then use schema.mode: verify on this host: %w", err)
	}
	return err
}

func (a *App) schema(e env, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(e.stderr, "omnistat schema: expected plan, apply or verify")
		return ExitError
	}
	switch args[0] {
	case "plan":
		return a.schemaPlan(e, args[1:])
	case "apply":
		return a.schemaApply(e)
	case "verify":
		return a.schemaVerify(e)
	}
	fmt.Fprintf(e.stderr, "omnistat schema: unknown subcommand %q (plan, apply, verify)\n", args[0])
	return ExitError
}

// schemaPlan is the dry-run (FR-020, FR-021). It never writes.
func (a *App) schemaPlan(e env, args []string) int {
	fs := flag.NewFlagSet("schema plan", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	asJSON := fs.Bool("json", false, "emit the plan as JSON (version 1)")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	p, err := a.prepare(e)
	if err != nil {
		return fail(e, err)
	}
	plan := schema.Diff(p.desired, p.current)
	if *asJSON {
		out, err := plan.JSON()
		if err != nil {
			return fail(e, err)
		}
		fmt.Fprintln(e.stdout, string(out))
	} else {
		fmt.Fprint(e.stdout, plan.Text())
	}
	switch {
	case len(plan.Conflicts) > 0:
		return ExitError
	case len(plan.Actions) > 0:
		return ExitChanges
	}
	return ExitOK
}

// schemaVerify fails if anything is missing and writes nothing (FR-010 verify).
func (a *App) schemaVerify(e env) int {
	p, err := a.prepare(e)
	if err != nil {
		return fail(e, err)
	}
	plan := schema.Diff(p.desired, p.current)
	if plan.Empty() {
		fmt.Fprintln(e.stdout, "schema complete")
		return ExitOK
	}
	fmt.Fprint(e.stdout, plan.Text())
	fmt.Fprintln(e.stderr, "omnistat: schema is incomplete; run `omnistat schema apply` with a token that can write the schema")
	return ExitError
}

// schemaApply shows the plan, pre-flights, then executes it (FR-022…027).
func (a *App) schemaApply(e env) int {
	p, err := a.prepare(e)
	if err != nil {
		return fail(e, err)
	}
	plan := schema.Diff(p.desired, p.current)
	fmt.Fprint(e.stdout, plan.Text())
	if len(plan.Conflicts) > 0 {
		return fail(e, &schema.ConflictError{Conflicts: plan.Conflicts})
	}
	if plan.Empty() {
		return ExitOK
	}
	// Best-effort permission pre-flight (FR-027): only hard auth failures matter.
	if _, err := p.api.MyPermissions(e.ctx); err != nil && (errors.Is(err, omni.ErrUnauthorized) || errors.Is(err, omni.ErrForbidden)) {
		return fail(e, explain(err))
	}

	res, err := schema.Apply(e.ctx, p.api, p.desired, p.current, e.log)
	for _, d := range res.Done {
		verb := "created"
		switch {
		case d.Skipped:
			verb = "already existed"
		case d.Action.Type == schema.BindAttribute:
			verb = "bound"
		case d.Action.Type == schema.AddListOption:
			verb = "added"
		}
		fmt.Fprintf(e.stdout, "✓ %s %s %s\n", verb, d.Action.Type, d.Action.Slug())
	}
	if err != nil {
		if res.Failed != nil {
			fmt.Fprintf(e.stdout, "✗ %s %s\n", res.Failed.Action.Type, res.Failed.Action.Slug())
		}
		fmt.Fprintf(e.stderr, "omnistat: %d of %d actions completed; re-run to continue\n", len(res.Done), len(plan.Actions))
		return fail(e, explain(err))
	}
	fmt.Fprintf(e.stdout, "\nschema reconciled: %d actions, %d re-reads\n", len(res.Done), res.Rereads)
	return ExitOK
}
