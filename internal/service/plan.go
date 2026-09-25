package service

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Step is one change install or uninstall makes. The dry-run prints Desc and
// Detail; the real run calls apply. Both come from the same Plan, so they
// cannot drift (006 FR-028, 007 FR-024).
type Step struct {
	Desc string
	// Detail is shown by the dry-run only, indented under Desc: the complete
	// unit file, for example (007 US-1/6). It never holds a setting's value.
	Detail string
	apply  func(ctx context.Context) (note string, err error)
}

// Plan is what install or uninstall will do, decided before anything changes.
type Plan struct {
	Steps []Step
	// Keep lists what is deliberately left in place (006 FR-020, 007 FR-021).
	Keep []string

	// NotInstalled: uninstall found no service.
	NotInstalled bool
	// Upgrade: install found its own service already installed.
	Upgrade bool
	// Binary is the installed program; ConfigPath the service's config file;
	// Unit the service definition file, where the platform has one.
	Binary     string
	ConfigPath string
	Unit       string
	// ConfigPresent: the config file exists (install creates a starter
	// otherwise, 007 FR-014).
	ConfigPresent bool
	// LogHint tells the operator where the service logs.
	LogHint string
	// Stored names the settings stored for the service, never their values.
	Stored []string
	// Checked is what the install pre-check found.
	Checked Checked
}

// Describe prints the steps as the dry-run shows them.
func (p *Plan) Describe(w io.Writer) {
	for _, s := range p.Steps {
		fmt.Fprintf(w, "  would %s\n", s.Desc)
		if s.Detail != "" {
			for _, line := range strings.Split(strings.TrimRight(s.Detail, "\n"), "\n") {
				fmt.Fprintf(w, "      %s\n", line)
			}
		}
	}
	for _, k := range p.Keep {
		fmt.Fprintf(w, "  would keep %s\n", k)
	}
}

// Apply runs the steps in order and stops at the first failure, naming it.
func (p *Plan) Apply(ctx context.Context, w io.Writer) error {
	for _, s := range p.Steps {
		note, err := s.apply(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Desc, err)
		}
		fmt.Fprintf(w, "  done: %s\n", s.Desc)
		if note != "" {
			fmt.Fprintf(w, "        %s\n", note)
		}
	}
	for _, k := range p.Keep {
		fmt.Fprintf(w, "  kept %s\n", k)
	}
	return nil
}

// Add appends a step. apply may return a note, printed under the step.
func (p *Plan) Add(desc string, apply func(ctx context.Context) (note string, err error)) {
	p.AddDetailed(desc, "", apply)
}

// AddDetailed appends a step whose dry-run also prints detail.
func (p *Plan) AddDetailed(desc, detail string, apply func(ctx context.Context) (note string, err error)) {
	p.Steps = append(p.Steps, Step{Desc: desc, Detail: detail, apply: apply})
}

// NoteIf returns note when cond holds, "" otherwise.
func NoteIf(cond bool, note string) string {
	if cond {
		return note
	}
	return ""
}
