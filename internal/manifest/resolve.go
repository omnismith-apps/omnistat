package manifest

import (
	"errors"
	"fmt"
	"sort"
)

// Overrides is what the operator may change about the declared schema
// (FR-006…009). Zero value means "defaults everywhere".
type Overrides struct {
	// HostTemplate replaces the default host template slug for every
	// attribute that has no more specific template override.
	HostTemplate string
	// Modules is keyed by module name.
	Modules map[string]ModuleOverride
}

// ModuleOverride applies to one module.
type ModuleOverride struct {
	// Template rebinds all of the module's attributes to this template slug.
	Template string
	// Attributes is keyed by Attribute.Key.
	Attributes map[string]AttributeOverride
}

// AttributeOverride applies to one attribute. Empty fields keep the default.
type AttributeOverride struct {
	Slug        string
	Template    string
	Name        string
	Description string
}

// Desired is the schema omnistat wants to exist: the union of all enabled
// manifests after overrides (FR-011). Slices are sorted by slug.
type Desired struct {
	// Host is the host template's slug after overrides.
	Host       string
	Templates  []DesiredTemplate
	Attributes []DesiredAttribute
}

// DesiredTemplate is a template that must exist.
type DesiredTemplate struct {
	Slug        string
	Name        string
	Description string
	// Entity, Module and Key describe an entity template (spec 011 FR-001):
	// the module that owns it and the manifest slug its provider targets it
	// by, which overrides never change.
	Entity bool
	Module string
	Key    string
}

// DesiredAttribute is an attribute that must exist and be bound to Templates.
type DesiredAttribute struct {
	Slug        string
	Name        string
	Description string
	Kind        Kind
	Options     []string
	// Templates are the slugs this attribute must be bound to (sorted).
	Templates []string
	// Module and Key identify the manifest entry for messages and logs.
	Module string
	Key    string
	// Platforms is the manifest's platform declaration, carried through
	// unchanged: overrides may move an attribute's slug or template, but
	// not where it can be collected (ADR-0007).
	Platforms []string
	// EntityTemplate is the manifest slug (DesiredTemplate.Key) of the entity
	// template the attribute sits on; empty for attributes of the host entity
	// (spec 011 FR-001, FR-006).
	EntityTemplate string
	// Target and Display are set for a reference: the template it points to
	// and the attribute shown for the referenced record, both resolved slugs
	// (spec 011 FR-003).
	Target  string
	Display string
}

// Template returns the desired template with the given slug, if any.
func (d Desired) Template(slug string) (DesiredTemplate, bool) {
	for _, t := range d.Templates {
		if t.Slug == slug {
			return t, true
		}
	}
	return DesiredTemplate{}, false
}

// Resolve applies overrides to validated manifests and returns the desired
// schema. Precedence per FR-007: attribute override > module override >
// manifest default > host template (overridable via Overrides.HostTemplate).
// Any slug collision after overrides, invalid override slug, or override that
// names an unknown module/attribute is an error (FR-009); all are reported.
func Resolve(manifests []Manifest, ov Overrides) (Desired, error) {
	var problems []error
	add := func(format string, args ...any) { problems = append(problems, fmt.Errorf(format, args...)) }

	host := HostTemplate
	if ov.HostTemplate != "" {
		if !ValidSlug(ov.HostTemplate) {
			add("host template slug %q must match ^[a-z][a-z0-9_]*$", ov.HostTemplate)
		} else {
			host = ov.HostTemplate
		}
	}

	byModule := map[string]Manifest{}
	for _, m := range manifests {
		byModule[m.Module] = m
	}
	for name, mo := range ov.Modules {
		m, ok := byModule[name]
		if !ok {
			add("override for unknown module %q", name)
			continue
		}
		if mo.Template != "" && !ValidSlug(mo.Template) {
			add("module %q: template slug %q must match ^[a-z][a-z0-9_]*$", name, mo.Template)
		}
		keys := map[string]bool{}
		for _, a := range m.Attributes {
			keys[a.Key] = true
		}
		for key, ao := range mo.Attributes {
			if !keys[key] {
				add("module %q has no attribute %q", name, key)
				continue
			}
			if ao.Slug != "" && !ValidSlug(ao.Slug) {
				add("module %q attribute %q: slug %q must match ^[a-z][a-z0-9_]*$", name, key, ao.Slug)
			}
			if ao.Template != "" && !ValidSlug(ao.Template) {
				add("module %q attribute %q: template slug %q must match ^[a-z][a-z0-9_]*$", name, key, ao.Template)
			}
		}
	}

	// Entity templates (spec 011 FR-005): a module-level template override
	// renames a module's entity template when all its attributes sit on it,
	// and is an error otherwise; nothing may resolve onto the host template,
	// and no two modules may share one.
	entitySlug := map[string]map[string]string{} // module → manifest slug → resolved slug
	entityOwner := map[string]string{}           // resolved slug → module
	for _, m := range manifests {
		mo := ov.Modules[m.Module]
		var ents []string
		for _, t := range m.Templates {
			if t.Entity {
				ents = append(ents, t.Slug)
			}
		}
		if len(ents) == 0 {
			continue
		}
		entitySlug[m.Module] = map[string]string{}
		rename := ""
		if mo.Template != "" && ValidSlug(mo.Template) {
			allOnOne := len(ents) == 1
			for _, a := range m.Attributes {
				if a.Template != ents[0] {
					allOnOne = false
				}
			}
			if allOnOne {
				rename = mo.Template
			} else {
				add("modules.%s.template: module %q declares an entity template, so its attributes cannot be moved together (spec 011 FR-005)", m.Module, m.Module)
			}
		}
		for _, e := range ents {
			slug := e
			if rename != "" {
				slug = rename
			}
			entitySlug[m.Module][e] = slug
			if slug == host {
				add("module %q: entity template %q cannot be the host template %q (spec 011 FR-005)", m.Module, e, host)
			}
			if prev, dup := entityOwner[slug]; dup && prev != m.Module {
				add("entity template %q is used by both %q and %q", slug, prev, m.Module)
			}
			entityOwner[slug] = m.Module
		}
	}

	templates := map[string]DesiredTemplate{}
	declare := func(t DesiredTemplate) {
		if _, exists := templates[t.Slug]; !exists {
			templates[t.Slug] = t
		}
	}
	declare(DesiredTemplate{Slug: host, Name: HostTemplateName, Description: "A machine reporting through omnistat"})
	for _, m := range manifests {
		for _, t := range m.Templates {
			dt := DesiredTemplate{Slug: t.Slug, Name: t.Name, Description: t.Description}
			if t.Entity {
				dt.Slug, dt.Entity, dt.Module, dt.Key = entitySlug[m.Module][t.Slug], true, m.Module, t.Slug
			}
			declare(dt)
		}
	}

	attrs := map[string]DesiredAttribute{}
	owner := map[string]string{} // slug → "module/key"
	for _, m := range manifests {
		mo := ov.Modules[m.Module]
		for _, a := range m.Attributes {
			ao := mo.Attributes[a.Key]
			da := DesiredAttribute{
				Slug:        a.Slug,
				Name:        a.Name,
				Description: a.Description,
				Kind:        a.Kind,
				Options:     append([]string(nil), a.Options...),
				Module:      m.Module,
				Key:         a.Key,
				Platforms:   append([]string(nil), a.Platforms...),
			}
			if ao.Slug != "" && ValidSlug(ao.Slug) {
				da.Slug = ao.Slug
			}
			if ao.Name != "" {
				da.Name = ao.Name
			}
			if ao.Description != "" {
				da.Description = ao.Description
			}
			tpl := host
			if ent, isEntity := entitySlug[m.Module][a.Template]; isEntity {
				// An entity template's attributes stay on it (spec 011 FR-005).
				tpl = ent
				da.EntityTemplate = a.Template
				if ao.Template != "" && ao.Template != ent {
					add("module %q attribute %q: it belongs to entity template %q and cannot be moved to %q (spec 011 FR-005)", m.Module, a.Key, ent, ao.Template)
				}
			} else {
				switch {
				case ao.Template != "" && ValidSlug(ao.Template):
					tpl = ao.Template
				case mo.Template != "" && ValidSlug(mo.Template):
					tpl = mo.Template
				case a.Template != "":
					tpl = a.Template
				}
				if owner, isEntity := entityOwner[tpl]; isEntity {
					add("module %q attribute %q: template %q is module %q's entity template", m.Module, a.Key, tpl, owner)
				}
			}
			if a.Kind == KindReference {
				da.Target = host
			}
			da.Templates = []string{tpl}
			if _, known := templates[tpl]; !known {
				declare(DesiredTemplate{Slug: tpl, Name: TitleFromSlug(tpl)})
			}

			id := m.Module + "/" + a.Key
			if prev, dup := owner[da.Slug]; dup {
				add("slug %q is mapped by both %s and %s", da.Slug, prev, id)
				continue
			}
			owner[da.Slug] = id
			attrs[da.Slug] = da
		}
	}
	// The host's label is what a host link shows (spec 011 FR-003): the
	// highest-ranked label attribute bound to the host template.
	display, rank := "", 0
	for _, m := range manifests {
		for _, a := range m.Attributes {
			if a.Label <= 0 {
				continue
			}
			da, ok := attrs[slugOf(m.Module, a.Key, owner)]
			if !ok || da.Templates[0] != host {
				continue
			}
			if a.Label > rank || (a.Label == rank && da.Slug < display) {
				display, rank = da.Slug, a.Label
			}
		}
	}
	for slug, da := range attrs {
		if da.Kind != KindReference {
			continue
		}
		if display == "" {
			add("module %q attribute %q: the host template has no label attribute for the host link to show (spec 011 FR-003)", da.Module, da.Key)
			continue
		}
		da.Display = display
		attrs[slug] = da
	}

	if err := errors.Join(problems...); err != nil {
		return Desired{}, err
	}

	d := Desired{Host: host}
	for _, t := range templates {
		d.Templates = append(d.Templates, t)
	}
	sort.Slice(d.Templates, func(i, j int) bool { return d.Templates[i].Slug < d.Templates[j].Slug })
	for _, a := range attrs {
		sort.Strings(a.Templates)
		d.Attributes = append(d.Attributes, a)
	}
	sort.Slice(d.Attributes, func(i, j int) bool { return d.Attributes[i].Slug < d.Attributes[j].Slug })
	return d, nil
}

// slugOf finds the resolved slug of module/key in the owner map (slug →
// "module/key").
func slugOf(module, key string, owner map[string]string) string {
	id := module + "/" + key
	for slug, o := range owner {
		if o == id {
			return slug
		}
	}
	return ""
}

// EntityTemplate returns the entity template module declares as key (its
// manifest slug), after overrides.
func (d Desired) EntityTemplate(module, key string) (DesiredTemplate, bool) {
	for _, t := range d.Templates {
		if t.Entity && t.Module == module && t.Key == key {
			return t, true
		}
	}
	return DesiredTemplate{}, false
}

// HostLink returns the host link attribute of the entity template with the
// given resolved slug (spec 011 FR-001).
func (d Desired) HostLink(templateSlug string) (DesiredAttribute, bool) {
	for _, a := range d.Attributes {
		if a.Kind == KindReference && a.Templates[0] == templateSlug {
			return a, true
		}
	}
	return DesiredAttribute{}, false
}

// Find returns the desired attribute declared by module/key, after overrides.
func (d Desired) Find(module, key string) (DesiredAttribute, bool) {
	for _, a := range d.Attributes {
		if a.Module == module && a.Key == key {
			return a, true
		}
	}
	return DesiredAttribute{}, false
}
