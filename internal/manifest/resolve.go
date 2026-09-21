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
	Templates  []DesiredTemplate
	Attributes []DesiredAttribute
}

// DesiredTemplate is a template that must exist.
type DesiredTemplate struct {
	Slug        string
	Name        string
	Description string
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

	templates := map[string]DesiredTemplate{}
	declare := func(t DesiredTemplate) {
		if _, exists := templates[t.Slug]; !exists {
			templates[t.Slug] = t
		}
	}
	declare(DesiredTemplate{Slug: host, Name: HostTemplateName, Description: "A machine reporting through omnistat"})
	for _, m := range manifests {
		for _, t := range m.Templates {
			declare(DesiredTemplate(t))
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
			switch {
			case ao.Template != "" && ValidSlug(ao.Template):
				tpl = ao.Template
			case mo.Template != "" && ValidSlug(mo.Template):
				tpl = mo.Template
			case a.Template != "":
				tpl = a.Template
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
	if err := errors.Join(problems...); err != nil {
		return Desired{}, err
	}

	var d Desired
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
