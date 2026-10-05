package schema

import (
	"sort"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// Diff computes the additive plan that turns cur into d (FR-013…019).
// Matching is by slug only; name and description never matter (FR-014/018).
// Options are compared exactly and case-sensitively; extra options in the
// project are ignored (FR-016).
func Diff(d manifest.Desired, cur Current) Plan {
	var p Plan

	// Attribute id → template ids currently bound, from the templates' side
	// (discovery does not list templates on attributes).
	boundTo := map[string][]string{}
	for _, t := range cur.Templates {
		for _, aid := range t.AttributeIDs {
			boundTo[aid] = append(boundTo[aid], t.ID)
		}
	}

	templates := append([]manifest.DesiredTemplate(nil), d.Templates...)
	sort.Slice(templates, func(i, j int) bool { return templates[i].Slug < templates[j].Slug })
	for _, t := range templates {
		if _, ok := cur.Templates[t.Slug]; ok {
			continue
		}
		p.Actions = append(p.Actions, Action{Type: CreateTemplate, Template: t.Slug, Name: t.Name, Description: t.Description})
	}

	attrs := append([]manifest.DesiredAttribute(nil), d.Attributes...)
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Slug < attrs[j].Slug })

	// References are created after every other attribute, so that the display
	// attribute they name exists first (spec 011 FR-003; FR-019 order kept).
	var refs, options, binds []Action
	for _, a := range attrs {
		want := TypeOf(a.Kind)
		existing, ok := cur.Attributes[a.Slug]
		if !ok {
			tpls := append([]string(nil), a.Templates...)
			sort.Strings(tpls)
			create := Action{
				Type: CreateAttribute, Attribute: a.Slug, Name: a.Name, Description: a.Description,
				Kind: a.Kind, Templates: tpls, Module: a.Module, Target: a.Target, Display: a.Display,
			}
			if a.Kind == manifest.KindReference {
				refs = append(refs, create)
				continue
			}
			p.Actions = append(p.Actions, create)
			for _, o := range a.Options {
				options = append(options, Action{Type: AddListOption, Attribute: a.Slug, Option: o, Module: a.Module})
			}
			continue
		}
		if existing.Type != want {
			p.Conflicts = append(p.Conflicts, Conflict{Slug: a.Slug, Module: a.Module, Expected: string(a.Kind), Actual: existing.Type})
			continue
		}
		if a.Kind == manifest.KindReference {
			// A host link must point at the host template; its display
			// attribute is cosmetic and never compared (spec 011 FR-004).
			if tpl, ok := cur.Templates[a.Target]; !ok || existing.RefTemplateID != tpl.ID {
				p.Conflicts = append(p.Conflicts, Conflict{Slug: a.Slug, Module: a.Module,
					Expected: "reference → " + a.Target, Actual: "reference → " + templateSlugOf(cur, existing.RefTemplateID)})
				continue
			}
		}
		if a.Kind == manifest.KindList {
			have := map[string]bool{}
			for _, o := range existing.Options {
				have[o] = true
			}
			for _, o := range a.Options {
				if !have[o] {
					options = append(options, Action{Type: AddListOption, Attribute: a.Slug, AttributeID: existing.ID, Option: o, Module: a.Module})
				}
			}
		}
		for _, tplSlug := range a.Templates {
			ids := append([]string(nil), boundTo[existing.ID]...)
			sort.Strings(ids)
			tpl, tplExists := cur.Templates[tplSlug]
			if !tplExists {
				// Template is created earlier in this plan; Apply appends its
				// id to TemplateIDs once known (existing bindings preserved).
				binds = append(binds, Action{Type: BindAttribute, Attribute: a.Slug, AttributeID: existing.ID, Template: tplSlug, TemplateIDs: ids, Module: a.Module})
				continue
			}
			already := false
			for _, id := range ids {
				if id == tpl.ID {
					already = true
					break
				}
			}
			if already {
				continue
			}
			all := append(append([]string(nil), ids...), tpl.ID)
			sort.Strings(all)
			binds = append(binds, Action{Type: BindAttribute, Attribute: a.Slug, AttributeID: existing.ID, Template: tplSlug, TemplateIDs: all, Module: a.Module})
		}
	}
	// Options keep manifest order within an attribute; attributes are already
	// sorted, so the whole list is deterministic (FR-019).
	p.Actions = append(p.Actions, refs...)
	p.Actions = append(p.Actions, options...)
	sort.SliceStable(binds, func(i, j int) bool {
		if binds[i].Attribute != binds[j].Attribute {
			return binds[i].Attribute < binds[j].Attribute
		}
		return binds[i].Template < binds[j].Template
	})
	p.Actions = append(p.Actions, binds...)
	sort.Slice(p.Conflicts, func(i, j int) bool { return p.Conflicts[i].Slug < p.Conflicts[j].Slug })
	return p
}

// templateSlugOf names a template by id for a conflict message, falling back
// to the id when the template has no slug omnistat can see.
func templateSlugOf(cur Current, id string) string {
	for slug, t := range cur.Templates {
		if t.ID == id {
			return slug
		}
	}
	if id == "" {
		return "(none)"
	}
	return id
}
