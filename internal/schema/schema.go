// Package schema reconciles the desired schema (from manifests) with the
// current schema of an Omnismith project: it computes a plan of additive
// actions (spec 001 FR-012…019) and executes it (FR-022…026). Diffing is pure;
// only Apply performs I/O, through the API interface.
package schema

import (
	"fmt"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// Current is the project schema as read from discovery, keyed by slug.
// Templates and attributes without a slug are invisible to omnistat.
type Current struct {
	Templates  map[string]CurrentTemplate
	Attributes map[string]CurrentAttribute
}

// CurrentTemplate is an existing template.
type CurrentTemplate struct {
	ID           string
	Slug         string
	Name         string
	AttributeIDs []string
}

// CurrentAttribute is an existing attribute. Type is the semantic type string
// reported by discovery (string, number, boolean, datetime, date, list,
// metric, reference, file, image, markdown).
type CurrentAttribute struct {
	ID      string
	Slug    string
	Name    string
	Type    string
	Options []string
}

// TypeOf maps a manifest kind to discovery's semantic type string (FR-014/015).
func TypeOf(k manifest.Kind) string {
	switch k {
	case manifest.KindText:
		return "string"
	default:
		return string(k)
	}
}

// ActionType is one of the four additive actions a plan may contain (FR-013).
type ActionType int

const (
	CreateTemplate ActionType = iota + 1
	CreateAttribute
	AddListOption
	BindAttribute
)

// String returns the snake_case name used in output and JSON.
func (t ActionType) String() string {
	switch t {
	case CreateTemplate:
		return "create_template"
	case CreateAttribute:
		return "create_attribute"
	case AddListOption:
		return "add_list_option"
	case BindAttribute:
		return "bind_attribute"
	}
	return fmt.Sprintf("action(%d)", int(t))
}

// MarshalText makes ActionType render as its name in JSON.
func (t ActionType) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// Action is one step of a plan. Which fields are set depends on Type:
//
//   - CreateTemplate: Template, Name, Description
//   - CreateAttribute: Attribute, Name, Description, Kind, Templates (slugs to bind at creation), Module
//   - AddListOption: Attribute, AttributeID (empty until the attribute is created), Option, Module
//   - BindAttribute: Attribute, AttributeID, Template, TemplateIDs (existing ∪ target), Module
type Action struct {
	Type        ActionType    `json:"type"`
	Template    string        `json:"template,omitempty"`
	Attribute   string        `json:"attribute,omitempty"`
	Name        string        `json:"name,omitempty"`
	Description string        `json:"description,omitempty"`
	Kind        manifest.Kind `json:"kind,omitempty"`
	Templates   []string      `json:"templates,omitempty"`
	Option      string        `json:"option,omitempty"`
	Module      string        `json:"module,omitempty"`
	AttributeID string        `json:"-"`
	TemplateIDs []string      `json:"-"`
}

// Slug is the primary slug the action is about (template for CreateTemplate,
// attribute otherwise).
func (a Action) Slug() string {
	if a.Type == CreateTemplate {
		return a.Template
	}
	return a.Attribute
}

// Conflict is an existing attribute whose kind contradicts a manifest (FR-015).
type Conflict struct {
	Slug     string `json:"attribute"`
	Module   string `json:"module"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// Error renders the conflict for humans.
func (c Conflict) Error() string {
	return fmt.Sprintf("attribute %q (module %s) exists as %s but the manifest declares %s", c.Slug, c.Module, c.Actual, c.Expected)
}

// Plan is the ordered result of a diff.
type Plan struct {
	Actions   []Action   `json:"actions"`
	Conflicts []Conflict `json:"conflicts"`
}

// Empty reports whether the plan has neither actions nor conflicts.
func (p Plan) Empty() bool { return len(p.Actions) == 0 && len(p.Conflicts) == 0 }

// Resolved maps desired slugs to platform ids after reconciliation (FR-025).
type Resolved struct {
	Templates  map[string]string
	Attributes map[string]string
}
