// Package manifest defines what a module declares about the Omnismith schema
// it needs (spec 001 FR-001…005) and turns the union of all enabled manifests,
// after the operator's overrides, into the desired schema (FR-006…011).
//
// Everything here is pure: no I/O, no SDK.
package manifest

import (
	"regexp"
	"strings"
)

// HostTemplate is the release-wide default template slug every host-level
// attribute attaches to unless remapped (FR-003). Changing it is a breaking
// change that requires an ADR.
const (
	HostTemplate     = "host"
	HostTemplateName = "Host"
)

// Kind is the semantic kind of an attribute as a module declares it. Only the
// kinds omnistat can publish are allowed (FR-005): references, files, images
// and markdown are not.
type Kind string

const (
	KindText     Kind = "text"
	KindNumber   Kind = "number"
	KindBoolean  Kind = "boolean"
	KindDate     Kind = "date"
	KindDatetime Kind = "datetime"
	KindList     Kind = "list"
	KindMetric   Kind = "metric"
)

// Valid reports whether k is a kind a manifest may declare.
func (k Kind) Valid() bool {
	switch k {
	case KindText, KindNumber, KindBoolean, KindDate, KindDatetime, KindList, KindMetric:
		return true
	}
	return false
}

// Kinds lists the valid kinds in a stable order (for messages and docs).
func Kinds() []Kind {
	return []Kind{KindText, KindNumber, KindBoolean, KindDate, KindDatetime, KindList, KindMetric}
}

// Manifest is a module's schema contract (FR-001).
type Manifest struct {
	// Module is the module name, e.g. "cpu" or "ip-address".
	Module string
	// Templates are non-host templates this module introduces. The host
	// template never needs declaring.
	Templates []Template
	// Attributes the module owns. Each attaches to Template, or to the host
	// template when Template is empty.
	Attributes []Attribute
}

// Template is a template a module introduces.
type Template struct {
	Slug        string
	Name        string
	Description string
}

// Attribute is one attribute a module owns.
type Attribute struct {
	// Key is the stable identifier the operator uses to override this
	// attribute ("usage"); it never changes even if the default slug does.
	Key         string
	Name        string
	Slug        string
	Kind        Kind
	Description string
	// Options are the ordered choices of a list attribute; required for
	// KindList, forbidden otherwise.
	Options []string
	// Template is the slug of the template to attach to; empty means host.
	Template string
}

var (
	slugRE   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	moduleRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// ValidSlug reports whether s satisfies FR-002's slug pattern.
func ValidSlug(s string) bool { return slugRE.MatchString(s) }

// ValidModuleName reports whether s is an acceptable module name.
func ValidModuleName(s string) bool { return moduleRE.MatchString(s) }

// TitleFromSlug derives a human name from a slug: "server_node" → "Server Node".
func TitleFromSlug(slug string) string {
	parts := strings.Split(slug, "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
