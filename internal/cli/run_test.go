package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/hostname"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/module/moduletest"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

var t0 = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

// harness is a production-like registry (machine-id, hostname, a scripted
// cpu module with a metric and a list) bound to a fake API and a fake clock.
type harness struct {
	srv      *omnitest.Server
	clock    *collect.FakeClock
	reg      *module.Registry
	host     atomic.Value // string
	cpuCalls atomic.Int32
	cpuFail  atomic.Bool
	cfg      string
	env      map[string]string
	logs     syncBuffer
	// maxPerMetric lowers the buffer bound (0 = spec default).
	maxPerMetric int
}

// syncBuffer is a bytes.Buffer safe for a writer and a reader in different goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newHarness(t *testing.T, cfg string) *harness {
	t.Helper()
	h := &harness{srv: omnitest.New(), clock: collect.NewFakeClock(t0)}
	t.Cleanup(h.srv.Close)
	h.host.Store("edge-fra-01")

	mid := machineid.New()
	mid.GOOS = "linux"
	mid.FS = fstest.MapFS{"etc/machine-id": {Data: []byte(rawID)}}
	hn := hostname.New()
	hn.Hostname = func() (string, error) { return h.host.Load().(string), nil }
	cpu := moduletest.WithProvider(moduletest.CPU(), 10*time.Second, func(context.Context) ([]module.Observation, error) {
		n := h.cpuCalls.Add(1)
		if h.cpuFail.Load() {
			return nil, errors.New("cpu unavailable")
		}
		return []module.Observation{{Key: "usage", Value: float64(n)}, {Key: "arch", Value: "arm64"}, {Key: "model", Value: "fake"}}, nil
	})
	h.reg = module.NewRegistry()
	h.reg.Register(mid, module.Required())
	h.reg.Register(hn)
	h.reg.Register(cpu)

	h.cfg = t.TempDir() + "/omnistat.yaml"
	if err := writeFile(h.cfg, cfg); err != nil {
		t.Fatal(err)
	}
	h.env = map[string]string{"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "OMNISMITH_PROJECT_ID": omnitest.ProjectID, "OMNISMITH_BASE_URL": h.srv.URL}
	return h
}

func (h *harness) app() *cli.App {
	return &cli.App{Registry: h.reg, Version: "t", Clock: h.clock, MaxPerMetric: h.maxPerMetric}
}

// logsSnapshot returns everything logged so far, including by a running daemon.
func (h *harness) logsSnapshot() string { return h.logs.String() }

// exec runs a command to completion.
func (h *harness) exec(ctx context.Context, args ...string) run {
	var out, errb bytes.Buffer
	code := h.app().Run(ctx, append([]string{"--config", h.cfg, "--log-level", "debug"}, args...), &out, &errb, func(k string) string { return h.env[k] })
	_, _ = h.logs.Write(errb.Bytes())
	return run{code, out.String(), errb.String()}
}

func (h *harness) entityID(t *testing.T) string {
	t.Helper()
	ents := h.srv.Entities()
	if len(ents) != 1 {
		t.Fatalf("want exactly one entity, got %d", len(ents))
	}
	return ents[0].ID
}

func (h *harness) writes(method, prefix string) []omnitest.Request {
	var out []omnitest.Request
	for _, r := range h.srv.Requests() {
		if r.Method == method && strings.HasPrefix(r.Path, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// US-1/2, FR-018: a fresh project → schema, entity and values in one invocation; order holds.
func TestRun_OnceFreshProject(t *testing.T) {
	h := newHarness(t, "")
	r := h.exec(context.Background(), "run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	id := h.entityID(t)
	if !strings.Contains(r.stdout, "schema reconciled") || !strings.Contains(r.stdout, "published 3 dimensions, 1 observations to entity "+id) {
		t.Fatalf("stdout: %s", r.stdout)
	}
	vals := h.srv.EntityValues(id)
	if vals["hostname"] != "edge-fra-01" || vals["cpu_model"] != "fake" || vals["machine_id"] != machineid.Derive(rawID) {
		t.Fatalf("values: %v", vals)
	}
	arch := ""
	for _, a := range h.srv.Attributes() {
		if a.Slug == "cpu_arch" {
			arch = a.OptionIDs["arm64"]
		}
	}
	if arch == "" || vals["cpu_arch"] != arch {
		t.Fatalf("list value must be the item id: %v (want %s)", vals["cpu_arch"], arch)
	}
	if m := h.srv.EntityMetrics(id)["cpu_usage_pct"]; len(m) != 1 || m[0].Value != "1" || m[0].UpdatedAt != "2026-09-22T10:00:00Z" {
		t.Fatalf("metrics: %+v", m)
	}
	// Order: schema writes, then entity create, then PATCH, then metrics.
	var order []string
	for _, q := range h.srv.Requests() {
		switch {
		case q.Method == "POST" && (q.Path == "/templates" || q.Path == "/attributes"):
			order = append(order, "schema")
		case q.Method == "POST" && strings.HasPrefix(q.Path, "/entities/template/"):
			order = append(order, "create")
		case q.Method == "PATCH" && strings.HasPrefix(q.Path, "/entities/"):
			order = append(order, "patch")
		case strings.HasSuffix(q.Path, "/metrics"):
			order = append(order, "metrics")
		}
	}
	joined := strings.Join(order, ",")
	if !strings.HasPrefix(joined, "schema,") || !strings.HasSuffix(joined, ",create,patch,metrics") || strings.Contains(strings.TrimSuffix(joined, ",create,patch,metrics"), "create") {
		t.Fatalf("order: %s", joined)
	}
	if strings.Contains(r.stdout+r.stderr, rawID) || strings.Contains(r.stderr, omnitest.Token) {
		t.Fatal("secret leaked")
	}

	// US-1/1: a second run reuses the entity, reconciles nothing, publishes again (FR-016).
	h.clock.Advance(time.Minute)
	r = h.exec(context.Background(), "run")
	if r.code != 0 || strings.Contains(r.stdout, "schema reconciled") || len(h.srv.Entities()) != 1 {
		t.Fatalf("second run: %+v entities=%d", r, len(h.srv.Entities()))
	}
	if hist := h.srv.EntityHistory(id); len(hist) != 6 {
		t.Fatalf("history: %d", len(hist))
	}
}

// US-1/3, FR-018: a failing module → the rest is published, exit 2.
func TestRun_OncePartial(t *testing.T) {
	h := newHarness(t, "")
	h.cpuFail.Store(true)
	r := h.exec(context.Background(), "run")
	if r.code != cli.ExitPartial || !strings.Contains(r.stderr, "1 module(s) failed to collect: cpu") || !strings.Contains(r.stderr, "cpu unavailable") {
		t.Fatalf("%+v", r)
	}
	id := h.entityID(t)
	if v := h.srv.EntityValues(id); v["hostname"] != "edge-fra-01" || v["cpu_model"] != nil {
		t.Fatalf("values: %v", v)
	}
	if len(h.srv.EntityMetrics(id)) != 0 {
		t.Fatal("no metrics expected")
	}
}

// FR-022: verify/off with a missing schema fail before any collection or write.
func TestRun_SchemaIncomplete(t *testing.T) {
	for _, mode := range []string{"verify", "off"} {
		h := newHarness(t, "schema:\n  mode: "+mode+"\n")
		r := h.exec(context.Background(), "run")
		if r.code != 1 || !strings.Contains(r.stderr, "schema is incomplete (schema.mode: "+mode+")") || !strings.Contains(r.stdout, "+ template host") {
			t.Fatalf("%s: %+v", mode, r)
		}
		if h.cpuCalls.Load() != 0 || len(h.srv.Entities()) != 0 || len(h.writes("POST", "/")) != 0 {
			t.Fatalf("%s: must not collect or write", mode)
		}
	}
	// Once applied, verify mode runs normally.
	h := newHarness(t, "schema:\n  mode: verify\n")
	if r := exec2(t, h.srv, h.reg, "schema", "apply"); r.code != 0 {
		t.Fatalf("apply: %+v", r)
	}
	if r := h.exec(context.Background(), "run"); r.code != 0 {
		t.Fatalf("verify run: %+v", r)
	}
}

// FR-021, US-4/1: dry-run writes nothing, shows plan, would-create and values; JSON needs dry-run.
func TestRun_DryRun(t *testing.T) {
	h := newHarness(t, "")
	r := h.exec(context.Background(), "run", "--dry-run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"+ template host", "(dry-run: schema not applied)", "would publish to (entity to be created): 2 dimensions, 1 observations",
		`hostname.hostname → hostname = "edge-fra-01" @ 2026-09-22T10:00:00Z`, `cpu.usage → cpu_usage_pct ← "1" @`} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout should contain %q:\n%s", want, r.stdout)
		}
	}
	// The list option cannot be mapped before the schema exists: dropped, logged, still no write.
	if !strings.Contains(r.stderr, `list option \"arm64\" of cpu_arch has no item`) {
		t.Errorf("expected the list drop in logs:\n%s", r.stderr)
	}
	for _, q := range h.srv.Requests() {
		if q.Method != "GET" {
			t.Fatalf("dry-run wrote: %+v", q)
		}
	}

	// After a real run, dry-run resolves the entity and prints JSON on request.
	if r := h.exec(context.Background(), "run"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	id := h.entityID(t)
	n := len(h.srv.Requests())
	r = h.exec(context.Background(), "run", "--dry-run", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var doc struct {
		Version    int              `json:"version"`
		Entity     string           `json:"entity"`
		Dimensions []map[string]any `json:"dimensions"`
		Metrics    []map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil || doc.Version != 1 || doc.Entity != id || len(doc.Dimensions) != 3 || len(doc.Metrics) != 1 {
		t.Fatalf("json: %v %s", err, r.stdout)
	}
	for _, q := range h.srv.Requests()[n:] {
		if q.Method != "GET" && !strings.HasPrefix(q.Path, "/entities/search/") {
			t.Fatalf("dry-run wrote: %+v", q)
		}
	}
	if r := h.exec(context.Background(), "run", "--json"); r.code != 1 || !strings.Contains(r.stderr, "--json requires --dry-run") {
		t.Fatalf("json without dry-run: %+v", r)
	}
}

// FR-003 (via Sources): an interval for a module that produces nothing is a config error.
func TestRun_IntervalWithoutProvider(t *testing.T) {
	h := newHarness(t, "modules:\n  machine-id:\n    interval: 1m\n")
	r := h.exec(context.Background(), "run")
	if r.code != 1 || !strings.Contains(r.stderr, "modules.machine-id.interval: module machine-id produces no values") {
		t.Fatalf("%+v", r)
	}
	if len(h.srv.Requests()) != 0 {
		t.Fatal("must fail before any network call")
	}
}

// FR-026/027: schedule logged at startup; values only at debug.
func TestRun_Logs(t *testing.T) {
	h := newHarness(t, "modules:\n  cpu:\n    interval: 15s\npublish:\n  interval: 45s\n")
	r := h.exec(context.Background(), "run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{`msg="module scheduled" module=hostname interval=5m0s`, `msg="module scheduled" module=cpu interval=15s`,
		`msg="publish scheduled" interval=45s daemon=false dry_run=false`, `msg=published dimensions=3 observations=1 requests=2 dropped=0`, `msg=identity source=linux-machine-id`} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("logs should contain %q:\n%s", want, r.stderr)
		}
	}
	// At info level the values never appear.
	var out, errb bytes.Buffer
	h.app().Run(context.Background(), []string{"--config", h.cfg, "run"}, &out, &errb, func(k string) string { return h.env[k] })
	if strings.Contains(errb.String(), "edge-fra-01") || strings.Contains(errb.String(), "level=DEBUG") {
		t.Fatalf("values leaked at info level:\n%s", errb.String())
	}
}
