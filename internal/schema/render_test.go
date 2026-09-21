package schema_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/schema"
)

// FR-020: human-readable and JSON (version 1) renderings.
func TestRender(t *testing.T) {
	p := schema.Diff(desired(), schema.Current{})
	p.Conflicts = append(p.Conflicts, schema.Conflict{Slug: "x", Module: "m", Expected: "text", Actual: "number"})

	text := p.Text()
	for _, want := range []string{"+ template host", "+ attribute cpu_model (text) → host", "+ option cpu_arch: amd64", "! conflict x", "6 actions", "1 conflict"} {
		if !strings.Contains(text, want) {
			t.Errorf("text rendering lacks %q:\n%s", want, text)
		}
	}

	raw, err := p.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version   int              `json:"version"`
		Actions   []map[string]any `json:"actions"`
		Conflicts []map[string]any `json:"conflicts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, raw)
	}
	if doc.Version != 1 || len(doc.Actions) != 6 || len(doc.Conflicts) != 1 {
		t.Fatalf("json doc: %+v", doc)
	}
	if doc.Actions[1]["type"] != "create_attribute" || doc.Actions[1]["attribute"] != "cpu_arch" || doc.Actions[1]["kind"] != "list" {
		t.Fatalf("action json: %+v", doc.Actions[1])
	}

	empty := schema.Plan{}
	if !strings.Contains(empty.Text(), "no changes") {
		t.Fatalf("empty plan text: %q", empty.Text())
	}
}
