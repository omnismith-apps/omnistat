package publish

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Line is one rendered observation as the dry-run shows it (FR-021).
type Line struct {
	Module string    `json:"module"`
	Key    string    `json:"key"`
	Slug   string    `json:"slug"`
	Value  any       `json:"value"`
	At     time.Time `json:"at"`
	// PendingOption: a list value whose option the dry-run's schema plan would
	// create; shown by its value, since it has no item id yet (FR-021).
	PendingOption bool `json:"pending_option,omitempty"`
}

// SkippedLine is one attribute — or, with an empty Key, one whole module —
// that this platform cannot collect (spec 004 FR-023, ADR-0007).
type SkippedLine struct {
	Module    string   `json:"module"`
	Key       string   `json:"key,omitempty"`
	Slug      string   `json:"slug,omitempty"`
	Platforms []string `json:"platforms,omitempty"`
}

// Printer writes what a publish would send. JSON emits one document per
// publish carrying a version field (FR-021). Skipped is fixed for the run and
// repeated in every document, so that each one stands alone for a script.
type Printer struct {
	W       io.Writer
	JSON    bool
	Skipped []SkippedLine
}

// Destination is the entity a printed publish would go to: the host, or a
// module-owned entity named by template and key (spec 011 FR-020).
type Destination struct {
	EntityID    string
	Module      string
	Template    string
	Key         string
	WouldCreate bool
}

type document struct {
	Version    int           `json:"version"`
	Entity     string        `json:"entity,omitempty"`
	Template   string        `json:"template,omitempty"`
	Key        string        `json:"key,omitempty"`
	Module     string        `json:"module,omitempty"`
	Create     bool          `json:"would_create,omitempty"`
	Dimensions []Line        `json:"dimensions"`
	Metrics    []Line        `json:"metrics"`
	Skipped    []SkippedLine `json:"skipped,omitempty"`
}

// Print renders one publish to one entity. An empty entity id means the
// entity does not exist yet (dry-run before the first real run).
func (p *Printer) Print(to Destination, dims, metrics []Line) {
	entityID := to.EntityID
	if p.JSON {
		doc := document{Version: 1, Entity: entityID, Template: to.Template, Key: to.Key, Module: to.Module, Create: to.WouldCreate,
			Dimensions: dims, Metrics: metrics, Skipped: p.Skipped}
		if doc.Dimensions == nil {
			doc.Dimensions = []Line{}
		}
		if doc.Metrics == nil {
			doc.Metrics = []Line{}
		}
		out, err := json.Marshal(doc)
		if err != nil {
			fmt.Fprintf(p.W, `{"version":1,"error":%q}`+"\n", err.Error())
			return
		}
		fmt.Fprintln(p.W, string(out))
		return
	}
	target := entityID
	if target == "" {
		target = "(entity to be created)"
	}
	if to.Template != "" {
		state := entityID
		if state == "" {
			state = "to be created"
		}
		target = fmt.Sprintf("%s %q (%s)", to.Template, to.Key, state)
	}
	fmt.Fprintf(p.W, "would publish to %s: %d dimensions, %d observations\n", target, len(dims), len(metrics))
	for _, l := range dims {
		note := ""
		if l.PendingOption {
			note = " (option created by schema apply)"
		}
		fmt.Fprintf(p.W, "  %s.%s → %s = %s @ %s%s\n", l.Module, l.Key, l.Slug, format(l.Value), l.At.UTC().Format(time.RFC3339), note)
	}
	for _, l := range metrics {
		fmt.Fprintf(p.W, "  %s.%s → %s ← %s @ %s\n", l.Module, l.Key, l.Slug, format(l.Value), l.At.UTC().Format(time.RFC3339))
	}
}

func format(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprint(v)
}
