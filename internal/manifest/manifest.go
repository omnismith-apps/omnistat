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
// kinds omnistat can publish are allowed (FR-005): files, images and markdown
// are not. A reference is allowed only as the host link of an entity template
// (spec 011 FR-002).
type Kind string

const (
	KindText     Kind = "text"
	KindNumber   Kind = "number"
	KindBoolean  Kind = "boolean"
	KindDate     Kind = "date"
	KindDatetime Kind = "datetime"
	KindList     Kind = "list"
	KindMetric   Kind = "metric"
	// KindReference links a record to another; only an entity template's host
	// link uses it (spec 011 FR-001, FR-002).
	KindReference Kind = "reference"
)

// Valid reports whether k is a kind a manifest may declare.
func (k Kind) Valid() bool {
	switch k {
	case KindText, KindNumber, KindBoolean, KindDate, KindDatetime, KindList, KindMetric, KindReference:
		return true
	}
	return false
}

// Kinds lists the valid kinds in a stable order (for messages and docs).
func Kinds() []Kind {
	return []Kind{KindText, KindNumber, KindBoolean, KindDate, KindDatetime, KindList, KindMetric, KindReference}
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
	// Entity marks an entity template (spec 011 FR-001): the module publishes
	// its attributes on that template to records of their own, identified by
	// external key and linked to the host by the one reference attribute the
	// module attaches to it. Its manifest slug is the name the provider uses
	// to target it, whatever the operator remaps it to.
	Entity bool
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
	// Target is the template a reference points to. Only HostTemplate (the
	// host template, after overrides) is accepted, and only for references
	// (spec 011 FR-002).
	Target string
	// Label ranks the attribute as its template's human-readable label: the
	// highest-ranked label bound to the host template is what a host link
	// displays (spec 011 FR-003). Zero is not a label.
	Label int
	// Platforms are the GOOS values on which this attribute can be
	// collected (ADR-0007). Empty means every platform. It never affects
	// the desired schema — an attribute is declared everywhere and merely
	// goes uncollected where the platform cannot report it.
	Platforms []string
}

// knownPlatforms is the set an attribute may name. It is deliberately the
// operating systems omnistat targets, so that a typo fails validation
// instead of silently gating an attribute off on every host.
var knownPlatforms = []string{"darwin", "linux", "windows"}

// KnownPlatforms lists the platforms a manifest may declare, sorted.
func KnownPlatforms() []string { return append([]string(nil), knownPlatforms...) }

// ValidPlatform reports whether s is a platform a manifest may declare.
func ValidPlatform(s string) bool {
	for _, p := range knownPlatforms {
		if p == s {
			return true
		}
	}
	return false
}

// Collectable reports whether an attribute declaring these platforms can be
// collected on goos. No declaration means everywhere (ADR-0007).
func Collectable(platforms []string, goos string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, p := range platforms {
		if p == goos {
			return true
		}
	}
	return false
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
