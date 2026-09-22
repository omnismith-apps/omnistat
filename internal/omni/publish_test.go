package omni_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/publish"
)

// Spec 003 FR-012: dimension update is one PATCH of backfill objects with
// string/bool values and RFC 3339 timestamps.
func TestUpdateEntity(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("hostname", "string")
	srv.AddAttribute("cpu_cores", "number")
	srv.AddAttribute("is_vm", "boolean")
	srv.AddTemplate("host", "Host", "hostname", "cpu_cores", "is_vm")
	id := srv.AddEntity("host", map[string]any{}, "")
	c := newClient(t, srv, omnitest.Token, omnitest.ProjectID)
	at := time.Date(2026, 9, 22, 10, 0, 1, 500_123_456, time.FixedZone("CEST", 2*3600))

	err := c.UpdateEntity(context.Background(), id, map[string]publish.Backfill{
		"hostname":  {Value: "edge-fra-01", At: at},
		"cpu_cores": {Value: "8", At: at},
		"is_vm":     {Value: true, At: at},
	})
	if err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	last := reqs[len(reqs)-1]
	if last.Method != "PATCH" || last.Path != "/entities/"+id {
		t.Fatalf("request: %+v", last)
	}
	var body map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(last.Body), &body); err != nil {
		t.Fatal(err)
	}
	attrs := body["attributes"]
	if attrs["hostname"]["value"] != "edge-fra-01" || attrs["cpu_cores"]["value"] != "8" || attrs["is_vm"]["value"] != true {
		t.Fatalf("values: %v", attrs)
	}
	// UTC, at most microsecond precision: the platform rejects nanoseconds (422).
	if ts, _ := attrs["hostname"]["updated_at"].(string); ts != "2026-09-22T08:00:01.500123Z" {
		t.Fatalf("updated_at must be UTC RFC 3339 with ≤ 6 fractional digits: %v", attrs["hostname"]["updated_at"])
	}
	if vals := srv.EntityValues(id); vals["hostname"] != "edge-fra-01" {
		t.Fatalf("stored: %v", vals)
	}
	if h := srv.EntityHistory(id); len(h) != 3 || h[0].UpdatedAt == "" {
		t.Fatalf("history: %+v", h)
	}

	// Unsupported Go type is a programming error, not a request.
	n := len(srv.Requests())
	if err := c.UpdateEntity(context.Background(), id, map[string]publish.Backfill{"cpu_cores": {Value: 8, At: at}}); err == nil || len(srv.Requests()) != n {
		t.Fatalf("int must be rejected before the request: %v", err)
	}
}

// FR-015: 404 and 422 map to sentinels with field errors.
func TestUpdateEntity_Errors(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("hostname", "string")
	srv.AddTemplate("host", "Host", "hostname")
	id := srv.AddEntity("host", map[string]any{}, "")
	c := newClient(t, srv, omnitest.Token, omnitest.ProjectID)
	at := time.Now()

	err := c.UpdateEntity(context.Background(), "nope", map[string]publish.Backfill{"hostname": {Value: "x", At: at}})
	if !errors.Is(err, omni.ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
	err = c.UpdateEntity(context.Background(), id, map[string]publish.Backfill{"hostname": {Value: "x", At: at}, "ghost": {Value: "y", At: at}})
	var ae *omni.APIError
	if !errors.Is(err, omni.ErrValidation) || !errors.As(err, &ae) || len(ae.Fields["attributes.ghost"]) != 1 {
		t.Fatalf("422: %v", err)
	}
}

// FR-012/017: metrics go by slug as strings with timestamps; 202 is success.
func TestIngestMetrics(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("cpu_usage", "metric")
	srv.AddAttribute("hostname", "string")
	srv.AddTemplate("host", "Host", "cpu_usage", "hostname")
	id := srv.AddEntity("host", map[string]any{}, "")
	c := newClient(t, srv, omnitest.Token, omnitest.ProjectID)
	at := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

	err := c.IngestMetrics(context.Background(), id, []publish.Metric{
		{Slug: "cpu_usage", Value: "24.5", At: at.Add(123_456_789 * time.Nanosecond)},
		{Slug: "cpu_usage", Value: "25", At: at.Add(10 * time.Second)},
	})
	if err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	last := reqs[len(reqs)-1]
	if last.Method != "POST" || last.Path != "/entities/"+id+"/metrics" {
		t.Fatalf("request: %+v", last)
	}
	var body struct {
		MetricValues []map[string]any `json:"metric_values"`
	}
	if err := json.Unmarshal([]byte(last.Body), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.MetricValues) != 2 || body.MetricValues[0]["attribute_slug"] != "cpu_usage" || body.MetricValues[0]["value"] != "24.5" || body.MetricValues[0]["updated_at"] != "2026-09-22T10:00:00.123456Z" || body.MetricValues[1]["updated_at"] != "2026-09-22T10:00:10Z" {
		t.Fatalf("body: %v", body.MetricValues)
	}
	if got := srv.EntityMetrics(id)["cpu_usage"]; len(got) != 2 || got[1].Value != "25" {
		t.Fatalf("stored: %+v", got)
	}

	if err := c.IngestMetrics(context.Background(), "nope", []publish.Metric{{Slug: "cpu_usage", Value: "1", At: at}}); !errors.Is(err, omni.ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
	err = c.IngestMetrics(context.Background(), id, []publish.Metric{{Slug: "hostname", Value: "1", At: at}})
	if !errors.Is(err, omni.ErrValidation) {
		t.Fatalf("422 for a non-metric: %v", err)
	}
}
