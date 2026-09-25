package publish_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/publish"
)

var t0 = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

type fixture struct {
	srv      *omnitest.Server
	api      *omni.Client
	entity   string
	items    map[string]map[string]string
	logs     *bytes.Buffer
	requests int
}

// newFixture sets up a host template with one attribute per kind and an entity.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	srv := omnitest.New()
	t.Cleanup(srv.Close)
	srv.AddAttribute("hostname", "string")
	srv.AddAttribute("cpu_cores", "number")
	srv.AddAttribute("is_vm", "boolean")
	srv.AddAttribute("installed", "date")
	srv.AddAttribute("booted", "datetime")
	srv.AddAttribute("arch", "list", "amd64", "arm64")
	srv.AddAttribute("cpu_usage", "metric")
	srv.AddAttribute("mem_usage", "metric")
	srv.AddTemplate("host", "Host", "hostname", "cpu_cores", "is_vm", "installed", "booted", "arch", "cpu_usage", "mem_usage")
	id := srv.AddEntity("host", map[string]any{}, "")
	api, err := omni.New(omni.Settings{BaseURL: srv.URL, Token: omnitest.Token, ProjectID: omnitest.ProjectID, Retries: 0, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]map[string]string{}
	for _, a := range srv.Attributes() {
		if a.OptionIDs != nil {
			items[a.Slug] = a.OptionIDs
		}
	}
	return &fixture{srv: srv, api: api, entity: id, items: items, logs: &bytes.Buffer{}, requests: len(srv.Requests())}
}

func (f *fixture) publisher() *publish.Publisher {
	return &publish.Publisher{API: f.api, EntityID: f.entity, ListItems: f.items,
		Log: slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
}

func (f *fixture) newRequests() []omnitest.Request {
	all := f.srv.Requests()
	out := all[f.requests:]
	f.requests = len(all)
	return out
}

func sample(kind manifest.Kind, slug string, v any, sec int) collect.Sample {
	return collect.Sample{Module: "m", Key: "k_" + slug, Slug: slug, Kind: kind, Value: v, At: t0.Add(time.Duration(sec) * time.Second)}
}

func batchOf(samples ...collect.Sample) collect.Batch {
	b := collect.NewBuffer(0)
	for _, s := range samples {
		b.Add(s)
	}
	return b.Snapshot()
}

// FR-012/012a/016: one PATCH with every kind rendered, list → item id, then one ingest.
func TestPublish_Shapes(t *testing.T) {
	f := newFixture(t)
	b := batchOf(
		sample(manifest.KindText, "hostname", "edge", 1),
		sample(manifest.KindNumber, "cpu_cores", 8.0, 1),
		sample(manifest.KindBoolean, "is_vm", true, 1),
		sample(manifest.KindDate, "installed", t0, 1),
		sample(manifest.KindDatetime, "booted", t0.Add(90*time.Minute), 1),
		sample(manifest.KindList, "arch", "arm64", 1),
		sample(manifest.KindMetric, "cpu_usage", 24.5, 1),
		sample(manifest.KindMetric, "cpu_usage", 1e21, 2),
		sample(manifest.KindMetric, "mem_usage", 0.1, 1),
	)
	res, err := f.publisher().Publish(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.newRequests()
	if len(reqs) != 2 || reqs[0].Method != "PATCH" || reqs[1].Path != "/entities/"+f.entity+"/metrics" {
		t.Fatalf("requests: %+v", reqs)
	}
	if res.Requests != 2 || res.Dimensions != 6 || res.Observations != 3 || res.Dropped != 0 {
		t.Fatalf("result: %+v", res)
	}
	vals := f.srv.EntityValues(f.entity)
	want := map[string]any{"hostname": "edge", "cpu_cores": "8", "is_vm": true, "installed": "2026-09-22", "booted": "2026-09-22T11:30:00Z", "arch": f.items["arch"]["arm64"]}
	for k, v := range want {
		if vals[k] != v {
			t.Errorf("%s = %#v, want %#v", k, vals[k], v)
		}
	}
	m := f.srv.EntityMetrics(f.entity)
	if len(m["cpu_usage"]) != 2 || m["cpu_usage"][0].Value != "24.5" || m["cpu_usage"][1].Value != "1000000000000000000000" || m["mem_usage"][0].Value != "0.1" || m["cpu_usage"][0].UpdatedAt != "2026-09-22T10:00:01Z" {
		t.Fatalf("metrics: %+v", m)
	}
	if strings.Join(res.Ack.Dims, ",") != "arch,booted,cpu_cores,hostname,installed,is_vm" || res.Ack.Metrics["cpu_usage"] != 2 || res.Ack.Metrics["mem_usage"] != 1 {
		t.Fatalf("ack: %+v", res.Ack)
	}

	// FR-016: the same value again is sent again (no local change detection).
	if _, err := f.publisher().Publish(context.Background(), batchOf(sample(manifest.KindText, "hostname", "edge", 5))); err != nil {
		t.Fatal(err)
	}
	if h := f.srv.EntityHistory(f.entity); len(h) != 7 || h[6].UpdatedAt != "2026-09-22T10:00:05Z" {
		t.Fatalf("history: %+v", h)
	}
}

// FR-012: empty batch → no request; NFR-003: 1 + ceil(n/1000) requests.
func TestPublish_ChunksAndEmpty(t *testing.T) {
	f := newFixture(t)
	res, err := f.publisher().Publish(context.Background(), batchOf())
	if err != nil || res.Requests != 0 || len(f.newRequests()) != 0 {
		t.Fatalf("empty: %+v %v", res, err)
	}
	var samples []collect.Sample
	samples = append(samples, sample(manifest.KindText, "hostname", "edge", 0))
	for i := 0; i < 2500; i++ {
		samples = append(samples, sample(manifest.KindMetric, "cpu_usage", float64(i), i))
	}
	res, err = f.publisher().Publish(context.Background(), batchOf(samples...))
	if err != nil {
		t.Fatal(err)
	}
	if res.Requests != 4 || res.Observations != 2500 || len(f.newRequests()) != 4 {
		t.Fatalf("chunking: %+v", res)
	}
	if got := f.srv.EntityMetrics(f.entity)["cpu_usage"]; len(got) != 2500 || got[2499].Value != "2499" {
		t.Fatalf("stored %d", len(got))
	}
	if res.Ack.Metrics["cpu_usage"] != 2500 {
		t.Fatalf("ack: %+v", res.Ack)
	}
}

// FR-012a: an option without an item is dropped (and acked) with an error log.
func TestPublish_UnknownListOption(t *testing.T) {
	f := newFixture(t)
	res, err := f.publisher().Publish(context.Background(), batchOf(
		sample(manifest.KindList, "arch", "riscv", 1),
		sample(manifest.KindText, "hostname", "edge", 1),
	))
	if err != nil || res.Dropped != 1 || res.Dimensions != 1 {
		t.Fatalf("result: %+v %v", res, err)
	}
	if strings.Join(res.Ack.Dims, ",") != "arch,hostname" {
		t.Fatalf("ack: %v", res.Ack.Dims)
	}
	if !strings.Contains(f.logs.String(), `list option \"riscv\" of arch has no item`) {
		t.Fatalf("logs: %s", f.logs)
	}
	if v := f.srv.EntityValues(f.entity); v["arch"] != nil || v["hostname"] != "edge" {
		t.Fatalf("values: %v", v)
	}
}

// FR-015: 404 → ErrEntityGone on either write.
func TestPublish_EntityGone(t *testing.T) {
	f := newFixture(t)
	p := f.publisher()
	p.EntityID = "missing"
	_, err := p.Publish(context.Background(), batchOf(sample(manifest.KindText, "hostname", "edge", 1)))
	if !errors.Is(err, publish.ErrEntityGone) || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("dims 404: %v", err)
	}
	_, err = p.Publish(context.Background(), batchOf(sample(manifest.KindMetric, "cpu_usage", 1.0, 1)))
	if !errors.Is(err, publish.ErrEntityGone) {
		t.Fatalf("metrics 404: %v", err)
	}
}

// FR-015: 422 on dims drops the named slugs and retries once; on a chunk drops the chunk.
func TestPublish_Rejected(t *testing.T) {
	f := newFixture(t)
	f.srv.FailNext(omnitest.Fault{Method: "PATCH", Status: 422, Body: `{"title":"Unprocessable","status":422,"errors":{"attributes.cpu_cores":["Must be a number."],"attributes.zzz":["Unknown."]}}`})
	res, err := f.publisher().Publish(context.Background(), batchOf(
		sample(manifest.KindText, "hostname", "edge", 1),
		sample(manifest.KindNumber, "cpu_cores", 8.0, 1),
	))
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.newRequests()
	if len(reqs) != 2 || res.Requests != 2 || res.Dimensions != 1 || res.Dropped != 1 {
		t.Fatalf("retry: %+v reqs=%d", res, len(reqs))
	}
	if strings.Contains(reqs[1].Body, "cpu_cores") || !strings.Contains(reqs[1].Body, "hostname") {
		t.Fatalf("retry body: %s", reqs[1].Body)
	}
	if strings.Join(res.Ack.Dims, ",") != "cpu_cores,hostname" {
		t.Fatalf("ack: %v", res.Ack.Dims)
	}
	if !strings.Contains(f.logs.String(), "dimension rejected and dropped") {
		t.Fatalf("logs: %s", f.logs)
	}

	// 422 with no recognisable field: error, nothing acked.
	f.srv.FailNext(omnitest.Fault{Method: "PATCH", Status: 422, Body: `{"title":"Unprocessable","status":422,"errors":{"rule":["Denied."]}}`})
	res, err = f.publisher().Publish(context.Background(), batchOf(sample(manifest.KindText, "hostname", "edge", 2)))
	if err == nil || len(res.Ack.Dims) != 0 {
		t.Fatalf("opaque 422: %+v %v", res, err)
	}

	// A rejected metric chunk is dropped, later chunks still go.
	f.newRequests()
	f.srv.FailNext(omnitest.Fault{Method: "POST", PathPrefix: "/entities/", Status: 422})
	var samples []collect.Sample
	for i := 0; i < 1500; i++ {
		samples = append(samples, sample(manifest.KindMetric, "cpu_usage", 1.0, i))
	}
	res, err = f.publisher().Publish(context.Background(), batchOf(samples...))
	if err != nil || res.Observations != 500 || res.Dropped != 1000 || res.Ack.Metrics["cpu_usage"] != 1500 {
		t.Fatalf("chunk 422: %+v %v", res, err)
	}
	if got := len(f.srv.EntityMetrics(f.entity)["cpu_usage"]); got != 500 {
		t.Fatalf("stored %d", got)
	}
}

// FR-009/FR-015: a transient failure returns the partial ack; the rest stays
// in the buffer and goes out on the next publish, timestamps intact.
func TestPublish_TransientKeepsBuffer(t *testing.T) {
	f := newFixture(t)
	buf := collect.NewBuffer(0)
	buf.Add(sample(manifest.KindText, "hostname", "edge", 1))
	for i := 0; i < 1500; i++ {
		buf.Add(sample(manifest.KindMetric, "cpu_usage", float64(i), i))
	}
	// PATCH succeeds, the first metric chunk gets a 503 (no client retries here).
	f.srv.FailNext(omnitest.Fault{Method: "POST", PathPrefix: "/entities/", Status: 503})
	b := buf.Snapshot()
	res, err := f.publisher().Publish(context.Background(), b)
	if err == nil || errors.Is(err, publish.ErrEntityGone) {
		t.Fatalf("want transient error, got %v", err)
	}
	if strings.Join(res.Ack.Dims, ",") != "hostname" || len(res.Ack.Metrics) != 0 || res.Requests != 2 {
		t.Fatalf("partial: %+v", res)
	}
	buf.Ack(b, res.Ack)
	b2 := buf.Snapshot()
	if len(b2.Dims) != 0 || len(b2.Metrics) != 1500 || !b2.Metrics[0].At.Equal(t0) {
		t.Fatalf("buffer after partial ack: dims=%d metrics=%d", len(b2.Dims), len(b2.Metrics))
	}
	res, err = f.publisher().Publish(context.Background(), b2)
	if err != nil || res.Observations != 1500 {
		t.Fatalf("second publish: %+v %v", res, err)
	}
	buf.Ack(b2, res.Ack)
	if !buf.Empty() {
		t.Fatal("buffer should be empty")
	}
	if got := f.srv.EntityMetrics(f.entity)["cpu_usage"]; got[0].UpdatedAt != "2026-09-22T10:00:00Z" {
		t.Fatalf("timestamp not preserved: %+v", got[0])
	}
}

// FR-021 (amended 2026-09-25): in a dry-run on a project whose schema is not
// applied yet, a list option the plan would create is shown with its value and
// marked, not dropped; an option neither present nor planned is still dropped.
func TestPublish_DryRunPendingOption(t *testing.T) {
	f := newFixture(t)
	var out, logs bytes.Buffer
	p := &publish.Publisher{EntityID: "", ListItems: map[string]map[string]string{},
		PendingOptions: map[string]map[string]bool{"arch": {"riscv": true}},
		Printer:        &publish.Printer{W: &out}, Log: slog.New(slog.NewTextHandler(&logs, nil))}
	res, err := p.Publish(context.Background(), batchOf(sample(manifest.KindList, "arch", "riscv", 1)))
	if err != nil || res.Dropped != 0 || res.Dimensions != 1 || len(f.newRequests()) != 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if want := `m.k_arch → arch = "riscv" @ 2026-09-22T10:00:01Z (option created by schema apply)`; !strings.Contains(out.String(), want) {
		t.Fatalf("want %q in:\n%s", want, out.String())
	}
	if logs.Len() != 0 {
		t.Fatalf("no error for a planned option:\n%s", logs.String())
	}

	out.Reset()
	p.Printer.JSON = true
	if _, err := p.Publish(context.Background(), batchOf(sample(manifest.KindList, "arch", "riscv", 1))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"value":"riscv"`) || !strings.Contains(out.String(), `"pending_option":true`) {
		t.Fatalf("json: %s", out.String())
	}

	// Not planned, not present: real drift, still dropped with an error (FR-012a).
	p.Printer.JSON = false
	res, _ = p.Publish(context.Background(), batchOf(sample(manifest.KindList, "arch", "sparc", 1)))
	if res.Dropped != 1 || !strings.Contains(logs.String(), `list option \"sparc\" of arch has no item`) {
		t.Fatalf("drift must still drop: %+v\n%s", res, logs.String())
	}

	// A real publish never uses planned options: it maps to item ids only.
	p.Printer = nil
	res, _ = p.Publish(context.Background(), batchOf(sample(manifest.KindList, "arch", "riscv", 1)))
	if res.Dropped != 1 {
		t.Fatalf("outside dry-run a pending option cannot be sent: %+v", res)
	}
}

// FR-021: the printer receives the rendered batch; nothing is sent; JSON carries a version.
func TestPublish_DryRun(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	p := &publish.Publisher{EntityID: "", ListItems: f.items, Printer: &publish.Printer{W: &out}}
	b := batchOf(
		sample(manifest.KindText, "hostname", "edge", 1),
		sample(manifest.KindList, "arch", "arm64", 1),
		sample(manifest.KindList, "arch", "riscv", 2), // latest wins in the buffer → dropped at render
		sample(manifest.KindMetric, "cpu_usage", 24.5, 3),
	)
	res, err := p.Publish(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.newRequests()) != 0 {
		t.Fatal("dry-run must not touch the API")
	}
	if res.Dimensions != 1 || res.Observations != 1 || res.Dropped != 1 || strings.Join(res.Ack.Dims, ",") != "arch,hostname" || res.Ack.Metrics["cpu_usage"] != 1 {
		t.Fatalf("result: %+v", res)
	}
	text := out.String()
	for _, want := range []string{"would publish to (entity to be created): 1 dimensions, 1 observations",
		`m.k_hostname → hostname = "edge" @ 2026-09-22T10:00:01Z`, `m.k_cpu_usage → cpu_usage ← "24.5" @ 2026-09-22T10:00:03Z`} {
		if !strings.Contains(text, want) {
			t.Errorf("text should contain %q:\n%s", want, text)
		}
	}

	out.Reset()
	p.EntityID, p.Printer.JSON = "e1", true
	if _, err := p.Publish(context.Background(), batchOf(sample(manifest.KindBoolean, "is_vm", true, 1))); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version    int              `json:"version"`
		Entity     string           `json:"entity"`
		Dimensions []map[string]any `json:"dimensions"`
		Metrics    []map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if doc.Version != 1 || doc.Entity != "e1" || len(doc.Dimensions) != 1 || doc.Dimensions[0]["value"] != true || doc.Dimensions[0]["slug"] != "is_vm" || doc.Metrics == nil || len(doc.Metrics) != 0 {
		t.Fatalf("json: %s", out.String())
	}
	// Empty batch prints nothing at all.
	out.Reset()
	if _, err := p.Publish(context.Background(), batchOf()); err != nil || out.Len() != 0 {
		t.Fatalf("empty: %q %v", out.String(), err)
	}
}
