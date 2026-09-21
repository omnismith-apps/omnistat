package manifest

import (
	"errors"
	"fmt"
	"strings"
)

// Validate checks every manifest against FR-001…005 and FR-002's cross-module
// uniqueness, reporting all problems at once (FR-004).
func Validate(manifests []Manifest) error {
	var problems []error
	add := func(format string, args ...any) { problems = append(problems, fmt.Errorf(format, args...)) }

	seenModule := map[string]bool{}
	slugOwner := map[string]string{} // slug → module

	for _, m := range manifests {
		if m.Module == "" {
			add("module name is empty")
		} else if !ValidModuleName(m.Module) {
			add("module name %q must match ^[a-z][a-z0-9-]*$", m.Module)
		}
		if seenModule[m.Module] {
			add("duplicate module %q", m.Module)
		}
		seenModule[m.Module] = true

		for _, t := range m.Templates {
			if !ValidSlug(t.Slug) {
				add("module %q: template slug %q must match ^[a-z][a-z0-9_]*$", m.Module, t.Slug)
			}
			if strings.TrimSpace(t.Name) == "" {
				add("module %q: template %q: name is empty", m.Module, t.Slug)
			}
		}

		seenKey := map[string]bool{}
		for _, a := range m.Attributes {
			where := fmt.Sprintf("module %q attribute %q", m.Module, a.Key)
			if a.Key == "" {
				add("module %q: attribute key is empty (slug %q)", m.Module, a.Slug)
			} else if seenKey[a.Key] {
				add("module %q: duplicate attribute key %q", m.Module, a.Key)
			}
			seenKey[a.Key] = true

			if strings.TrimSpace(a.Name) == "" {
				add("%s: name is empty", where)
			}
			if !ValidSlug(a.Slug) {
				add("%s: slug %q must match ^[a-z][a-z0-9_]*$", where, a.Slug)
			} else if owner, dup := slugOwner[a.Slug]; dup && owner != m.Module {
				add("slug %q declared by both %q and %q", a.Slug, owner, m.Module)
			} else if dup {
				add("module %q: slug %q declared twice", m.Module, a.Slug)
			} else {
				slugOwner[a.Slug] = m.Module
			}
			if !a.Kind.Valid() {
				add("%s: kind %q is not one of %v", where, a.Kind, Kinds())
			}
			switch {
			case a.Kind == KindList && len(a.Options) == 0:
				add("%s: list attribute needs options", where)
			case a.Kind != KindList && len(a.Options) > 0:
				add("%s: only list attributes may have options", where)
			}
			seenOpt := map[string]bool{}
			for _, o := range a.Options {
				if strings.TrimSpace(o) == "" {
					add("%s: empty option", where)
					continue
				}
				if seenOpt[o] {
					add("%s: duplicate option %q", where, o)
				}
				seenOpt[o] = true
			}
			if a.Template != "" && !ValidSlug(a.Template) {
				add("%s: template slug %q must match ^[a-z][a-z0-9_]*$", where, a.Template)
			}
		}
	}
	return errors.Join(problems...)
}
