package winsvc

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Step is one change install or uninstall makes. The dry-run prints Desc; the
// real run calls apply. Both come from the same Plan, so they cannot drift
// (FR-028).
type Step struct {
	Desc  string
	apply func(ctx context.Context) (note string, err error)
}

// Plan is what install or uninstall will do, decided before anything changes.
type Plan struct {
	Steps []Step
	// Keep lists what is deliberately left in place (FR-020).
	Keep []string

	// NotInstalled: uninstall found no service (US-6/2).
	NotInstalled bool
	// Upgrade: install found its own service already installed (FR-017).
	Upgrade bool
	// Binary is the installed program; ConfigPath the service's config file.
	Binary     string
	ConfigPath string
	// Stored names the settings stored for the service, never their values (FR-016).
	Stored []string
	// Checked is what the install pre-check found (FR-006).
	Checked Checked
}

// Describe prints the steps as the dry-run shows them (FR-028).
func (p *Plan) Describe(w io.Writer) {
	for _, s := range p.Steps {
		fmt.Fprintf(w, "  would %s\n", s.Desc)
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

func (p *Plan) add(desc string, apply func(ctx context.Context) (string, error)) {
	p.Steps = append(p.Steps, Step{Desc: desc, apply: apply})
}

// winJoin joins a Windows directory and a file name on any build, so plans read
// the same in tests on Linux as on the host they are for.
func winJoin(dir, name string) string {
	return strings.TrimRight(dir, `\/`) + `\` + name
}

// samePath compares Windows paths: case-insensitively, ignoring quotes.
func samePath(a, b string) bool {
	return strings.EqualFold(strings.Trim(a, `"`), strings.Trim(b, `"`))
}

// ours reports whether an existing service is the one omnistat installs (FR-019).
func ours(inst Installed, target string) error {
	if inst.Exists && !samePath(inst.Binary, target) {
		return fmt.Errorf("%w (it runs %s); nothing was changed", ErrForeignService, inst.Binary)
	}
	return nil
}

func noteIf(cond bool, note string) string {
	if cond {
		return note
	}
	return ""
}
