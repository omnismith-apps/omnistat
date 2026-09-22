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
}

// Printer writes what a publish would send. JSON emits one document per
// publish carrying a version field (FR-021).
type Printer struct {
	W    io.Writer
	JSON bool
}

type document struct {
	Version    int    `json:"version"`
	Entity     string `json:"entity,omitempty"`
	Dimensions []Line `json:"dimensions"`
	Metrics    []Line `json:"metrics"`
}

// Print renders one publish. An empty entity id means the entity does not
// exist yet (dry-run before the first real run).
func (p *Printer) Print(entityID string, dims, metrics []Line) {
	if p.JSON {
		doc := document{Version: 1, Entity: entityID, Dimensions: dims, Metrics: metrics}
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
	fmt.Fprintf(p.W, "would publish to %s: %d dimensions, %d observations\n", target, len(dims), len(metrics))
	for _, l := range dims {
		fmt.Fprintf(p.W, "  %s.%s → %s = %s @ %s\n", l.Module, l.Key, l.Slug, format(l.Value), l.At.UTC().Format(time.RFC3339))
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
