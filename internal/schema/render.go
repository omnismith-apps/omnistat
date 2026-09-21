package schema

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PlanVersion is the JSON plan format version (FR-020). Best effort until 1.0.
const PlanVersion = 1

// Text renders the plan for humans.
func (p Plan) Text() string {
	if p.Empty() {
		return "no changes\n"
	}
	var b strings.Builder
	for _, a := range p.Actions {
		switch a.Type {
		case CreateTemplate:
			fmt.Fprintf(&b, "+ template %s (%q)\n", a.Template, a.Name)
		case CreateAttribute:
			fmt.Fprintf(&b, "+ attribute %s (%s) → %s  [%s]\n", a.Attribute, a.Kind, strings.Join(a.Templates, ", "), a.Module)
		case AddListOption:
			fmt.Fprintf(&b, "+ option %s: %s  [%s]\n", a.Attribute, a.Option, a.Module)
		case BindAttribute:
			fmt.Fprintf(&b, "~ bind %s → %s  [%s]\n", a.Attribute, a.Template, a.Module)
		}
	}
	for _, c := range p.Conflicts {
		fmt.Fprintf(&b, "! conflict %s: exists as %s, module %s declares %s\n", c.Slug, c.Actual, c.Module, c.Expected)
	}
	fmt.Fprintf(&b, "\n%s, %s\n", plural(len(p.Actions), "action"), plural(len(p.Conflicts), "conflict"))
	return b.String()
}

// JSON renders the plan as a versioned document.
func (p Plan) JSON() ([]byte, error) {
	doc := struct {
		Version   int        `json:"version"`
		Actions   []Action   `json:"actions"`
		Conflicts []Conflict `json:"conflicts"`
	}{PlanVersion, p.Actions, p.Conflicts}
	if doc.Actions == nil {
		doc.Actions = []Action{}
	}
	if doc.Conflicts == nil {
		doc.Conflicts = []Conflict{}
	}
	return json.MarshalIndent(doc, "", "  ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
