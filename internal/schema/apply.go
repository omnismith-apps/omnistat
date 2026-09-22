package schema

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/manifest"
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
// (FR-022…026). It computes the plan itself so that display and execution can
// never disagree; the caller typically showed Diff(desired, cur) first.
//
// On a create refused with ErrAlreadyExists the schema is re-read: if the
// object is now present the action is skipped and the remaining plan is
// recomputed; if it is absent the original error is returned (FR-024). Any
// other error stops the run; re-running is safe (FR-026).
func Apply(ctx context.Context, api API, desired manifest.Desired, cur Current, log *slog.Logger) (Result, error) {
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
	// A re-read is due after binds (verification, below) and after list
	// options were added, whose item ids only discovery reports (spec 003 FR-012a).
	reread := false

	for len(remaining) > 0 {
		a := remaining[0]
		remaining = remaining[1:]
		err := execute(ctx, api, a, &res.Resolved)
		if err == nil {
			res.Done = append(res.Done, ActionResult{Action: a})
			reread = reread || a.Type == BindAttribute || a.Type == AddListOption
			log.Info("schema action applied", "action", a.Type.String(), "module", a.Module, "attribute", a.Attribute, "template", a.Template, "option", a.Option)
			continue
		}
		if errors.Is(err, ErrAlreadyExists) && res.Rereads < maxRereads {
			res.Rereads++
			fresh, rerr := api.ReadSchema(ctx)
			if rerr != nil {
				res.Failed = &ActionResult{Action: a, Err: rerr}
				return res, fmt.Errorf("re-read schema after %s %s: %w", a.Type, a.Slug(), rerr)
			}
			if !exists(fresh, a) {
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
			res.Resolved = resolvedFrom(fresh)
			remaining = replan.Actions
			continue
		}
		res.Failed = &ActionResult{Action: a, Err: err}
		log.Error("schema action failed", "action", a.Type.String(), "module", a.Module, "attribute", a.Attribute, "template", a.Template, "error", err)
		return res, fmt.Errorf("%s %s: %w", a.Type, a.Slug(), err)
	}

	// Binds are read-modify-write on the attribute side; verify once after
	// them so a binding lost to a concurrent bind is redone (plan risk note).
	// The same read refreshes Resolved with newly created list item ids.
	if reread {
		fresh, err := api.ReadSchema(ctx)
		if err != nil {
			return res, fmt.Errorf("verify schema: %w", err)
		}
		res.Resolved = resolvedFrom(fresh)
		if replan := Diff(desired, fresh); len(replan.Actions) > 0 && len(replan.Conflicts) == 0 {
			for _, a := range replan.Actions {
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
		return api.AddListOption(ctx, id, a.Option)
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
