package schema

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/settle"
)

// maxRereads bounds the fleet-race loop (FR-024).
const maxRereads = 5

// ConflictError is returned by Apply when the plan has conflicts (FR-022).
type ConflictError struct{ Conflicts []Conflict }

func (e *ConflictError) Error() string {
	msgs := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		msgs[i] = c.Error()
	}
	return "schema conflicts, nothing written: " + strings.Join(msgs, "; ")
}

// ActionResult is one executed (or skipped) action.
type ActionResult struct {
	Action Action
	// Skipped is true when the object turned out to exist already (FR-024).
	Skipped bool
	Err     error
}

// Result reports what Apply did.
type Result struct {
	Done     []ActionResult
	Failed   *ActionResult // set when Apply returned an error mid-plan
	Rereads  int
	Resolved Resolved
}

// Apply reconciles desired onto the project whose current schema is cur
// (FR-022…026), waiting for writes to become visible with the default settle
// policy. It computes the plan itself so that display and execution can never
// disagree; the caller typically showed Diff(desired, cur) first.
func Apply(ctx context.Context, api API, desired manifest.Desired, cur Current, log *slog.Logger) (Result, error) {
	return ApplyWith(ctx, api, desired, cur, log, settle.Default())
}

// ApplyWith is Apply with an explicit settle policy; tests pass one that never
// sleeps.
//
// On a create refused with ErrAlreadyExists the schema is re-read: if the
// object is present the action is skipped and the remaining plan is
// recomputed; if it is absent the original error is returned (FR-024).
// Because the platform processes writes asynchronously, "absent" is only
// concluded once the re-read has waited out the settle policy: the object
// that caused the refusal may simply not be visible yet.
//
// For the same reason no re-read is trusted to show this run's own writes.
// Ids learned from write responses are kept even when a later read lacks them
// (FR-025), and an object this run created is never created again because a
// read did not show it yet. Binds are the exception: re-binding is additive
// and idempotent, and it is how a bind lost to a concurrent bind is redone.
//
// Any other error stops the run; re-running is safe (FR-026).
func ApplyWith(ctx context.Context, api API, desired manifest.Desired, cur Current, log *slog.Logger, p settle.Policy) (Result, error) {
	if log == nil {
		log = slog.Default()
	}
	var res Result
	plan := Diff(desired, cur)
	if len(plan.Conflicts) > 0 {
		return res, &ConflictError{Conflicts: plan.Conflicts}
	}
	res.Resolved = resolvedFrom(cur)
	remaining := plan.Actions
	// A re-read is due after binds (verification, below). List item ids come
	// from the create responses (spec 003 FR-012a), not from a re-read that may
	// not show them yet.
	reread := false

	for len(remaining) > 0 {
		a := remaining[0]
		remaining = remaining[1:]
		err := execute(ctx, api, a, &res.Resolved)
		if err == nil {
			res.Done = append(res.Done, ActionResult{Action: a})
			reread = reread || a.Type == BindAttribute
			log.Info("schema action applied", "action", a.Type.String(), "module", a.Module, "attribute", a.Attribute, "template", a.Template, "option", a.Option)
			continue
		}
		if errors.Is(err, ErrAlreadyExists) && res.Rereads < maxRereads {
			res.Rereads++
			fresh, present, rerr := settle.Until(ctx, p, api.ReadSchema, func(c Current) bool { return exists(c, a) })
			if rerr != nil {
				res.Failed = &ActionResult{Action: a, Err: rerr}
				return res, fmt.Errorf("re-read schema after %s %s: %w", a.Type, a.Slug(), rerr)
			}
			if !present {
				res.Failed = &ActionResult{Action: a, Err: err}
				return res, err
			}
			log.Info("schema object already exists, re-planning", "action", a.Type.String(), "module", a.Module, "attribute", a.Attribute, "template", a.Template, "option", a.Option)
			res.Done = append(res.Done, ActionResult{Action: a, Skipped: true})
			replan := Diff(desired, fresh)
			if len(replan.Conflicts) > 0 {
				res.Failed = &ActionResult{Action: a, Err: err}
				return res, &ConflictError{Conflicts: replan.Conflicts}
			}
			res.Resolved = merge(resolvedFrom(fresh), res.Resolved)
			remaining = notYetDone(replan.Actions, res.Done)
			continue
		}
		res.Failed = &ActionResult{Action: a, Err: err}
		log.Error("schema action failed", "action", a.Type.String(), "module", a.Module, "attribute", a.Attribute, "template", a.Template, "error", err)
		return res, fmt.Errorf("%s %s: %w", a.Type, a.Slug(), err)
	}

	// Binds are read-modify-write on the attribute side; verify once after
	// them so a binding lost to a concurrent bind is redone (plan risk note).
	// A lagging read may also show this run's own binds as missing; redoing
	// those is harmless. Nothing else this run created is redone.
	if reread {
		fresh, err := api.ReadSchema(ctx)
		if err != nil {
			return res, fmt.Errorf("verify schema: %w", err)
		}
		res.Resolved = merge(resolvedFrom(fresh), res.Resolved)
		if replan := Diff(desired, fresh); len(replan.Actions) > 0 && len(replan.Conflicts) == 0 {
			for _, a := range notYetDone(replan.Actions, res.Done) {
				if err := execute(ctx, api, a, &res.Resolved); err != nil && !errors.Is(err, ErrAlreadyExists) {
					res.Failed = &ActionResult{Action: a, Err: err}
					return res, fmt.Errorf("%s %s: %w", a.Type, a.Slug(), err)
				}
				res.Done = append(res.Done, ActionResult{Action: a})
			}
		}
	}
	return res, nil
}

// notYetDone drops the actions this run already performed — whether it
// created the object or found it already there — except binds, which a
// verification may legitimately need to redo. A read that lags behind this
// run's writes makes its objects look missing; they are not (FR-024, FR-025).
func notYetDone(actions []Action, done []ActionResult) []Action {
	did := map[string]bool{}
	for _, d := range done {
		did[d.Action.Type.String()+" "+d.Action.Slug()] = true
	}
	var out []Action
	for _, a := range actions {
		if a.Type != BindAttribute && did[a.Type.String()+" "+a.Slug()] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// merge overlays ids this run learned from its own writes onto ids read from
// discovery, so that a read which does not show those writes yet cannot drop
// them (FR-025).
func merge(read, own Resolved) Resolved {
	for slug, id := range own.Templates {
		read.Templates[slug] = id
	}
	for slug, id := range own.Attributes {
		read.Attributes[slug] = id
	}
	for slug, items := range own.ListItems {
		if read.ListItems[slug] == nil {
			read.ListItems[slug] = map[string]string{}
		}
		for v, id := range items {
			read.ListItems[slug][v] = id
		}
	}
	return read
}

func execute(ctx context.Context, api API, a Action, r *Resolved) error {
	switch a.Type {
	case CreateTemplate:
		id, err := api.CreateTemplate(ctx, CreateTemplateParams{Slug: a.Template, Name: a.Name, Description: a.Description})
		if err != nil {
			return err
		}
		r.Templates[a.Template] = id
	case CreateAttribute:
		ids := make([]string, 0, len(a.Templates))
		for _, slug := range a.Templates {
			id, ok := r.Templates[slug]
			if !ok {
				return fmt.Errorf("template %q has no id yet (plan order violated)", slug)
			}
			ids = append(ids, id)
		}
		id, err := api.CreateAttribute(ctx, CreateAttributeParams{Slug: a.Attribute, Name: a.Name, Description: a.Description, Kind: a.Kind, TemplateIDs: ids})
		if err != nil {
			return err
		}
		r.Attributes[a.Attribute] = id
	case AddListOption:
		id := a.AttributeID
		if id == "" {
			id = r.Attributes[a.Attribute]
		}
		if id == "" {
			return fmt.Errorf("attribute %q has no id yet (plan order violated)", a.Attribute)
		}
		item, err := api.AddListOption(ctx, id, a.Option)
		if err != nil {
			return err
		}
		if r.ListItems[a.Attribute] == nil {
			r.ListItems[a.Attribute] = map[string]string{}
		}
		r.ListItems[a.Attribute][a.Option] = item
	case BindAttribute:
		tplID, ok := r.Templates[a.Template]
		if !ok {
			return fmt.Errorf("template %q has no id yet (plan order violated)", a.Template)
		}
		ids := append([]string(nil), a.TemplateIDs...)
		found := false
		for _, id := range ids {
			if id == tplID {
				found = true
			}
		}
		if !found {
			ids = append(ids, tplID)
		}
		sort.Strings(ids)
		return api.BindAttribute(ctx, a.AttributeID, ids)
	default:
		return fmt.Errorf("unknown action %v", a.Type)
	}
	return nil
}

// exists reports whether the object an action would create is present in cur.
func exists(cur Current, a Action) bool {
	switch a.Type {
	case CreateTemplate:
		_, ok := cur.Templates[a.Template]
		return ok
	case CreateAttribute:
		_, ok := cur.Attributes[a.Attribute]
		return ok
	case AddListOption:
		attr, ok := cur.Attributes[a.Attribute]
		if !ok {
			return false
		}
		for _, o := range attr.Options {
			if o == a.Option {
				return true
			}
		}
		return false
	case BindAttribute:
		tpl, ok := cur.Templates[a.Template]
		attr, ok2 := cur.Attributes[a.Attribute]
		if !ok || !ok2 {
			return false
		}
		for _, id := range tpl.AttributeIDs {
			if id == attr.ID {
				return true
			}
		}
		return false
	}
	return false
}

func resolvedFrom(cur Current) Resolved {
	r := Resolved{Templates: map[string]string{}, Attributes: map[string]string{}, ListItems: map[string]map[string]string{}}
	for slug, t := range cur.Templates {
		r.Templates[slug] = t.ID
	}
	for slug, a := range cur.Attributes {
		r.Attributes[slug] = a.ID
		if len(a.OptionIDs) > 0 {
			items := make(map[string]string, len(a.OptionIDs))
			for v, id := range a.OptionIDs {
				items[v] = id
			}
			r.ListItems[slug] = items
		}
	}
	return r
}
