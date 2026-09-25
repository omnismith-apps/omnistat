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
	"github.com/omnismith-apps/omnistat/internal/manifest"
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
	srv        *omnitest.Server
	clock      *collect.FakeClock
	reg        *module.Registry
	host       atomic.Value // string
	probeCalls atomic.Int32
	probeFail  atomic.Bool
	cfg        string
	env        map[string]string
	logs       syncBuffer
	// maxPerMetric lowers the buffer bound (0 = spec default).
	maxPerMetric int
	// goos overrides the platform the collectable check gates on (004 FR-019).
	goos string
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
	probe := moduletest.WithProvider(moduletest.Probe(), 10*time.Second, func(context.Context) ([]module.Observation, error) {
		n := h.probeCalls.Add(1)
		if h.probeFail.Load() {
			return nil, errors.New("probe unavailable")
		}
		return []module.Observation{{Key: "usage", Value: float64(n)}, {Key: "arch", Value: "arm64"}, {Key: "model", Value: "fake"}}, nil
	})
	h.reg = module.NewRegistry()
	h.reg.Register(mid, module.Required())
	h.reg.Register(hn)
	h.reg.Register(probe)

	h.cfg = t.TempDir() + "/omnistat.yaml"
	if err := writeFile(h.cfg, cfg); err != nil {
		t.Fatal(err)
	}
	h.env = map[string]string{"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "OMNISMITH_PROJECT_ID": omnitest.ProjectID, "OMNISMITH_BASE_URL": h.srv.URL}
	return h
}

func (h *harness) app() *cli.App {
	return &cli.App{Registry: h.reg, Version: "t", Clock: h.clock, MaxPerMetric: h.maxPerMetric, GOOS: h.goos}
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
	if vals["hostname"] != "edge-fra-01" || vals["probe_model"] != "fake" || vals["machine_id"] != machineid.Derive(rawID) {
		t.Fatalf("values: %v", vals)
	}
	arch := ""
	for _, a := range h.srv.Attributes() {
		if a.Slug == "probe_arch" {
			arch = a.OptionIDs["arm64"]
		}
	}
	if arch == "" || vals["probe_arch"] != arch {
		t.Fatalf("list value must be the item id: %v (want %s)", vals["probe_arch"], arch)
	}
	if m := h.srv.EntityMetrics(id)["probe_usage_pct"]; len(m) != 1 || m[0].Value != "1" || m[0].UpdatedAt != "2026-09-22T10:00:00Z" {
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
	h.probeFail.Store(true)
	r := h.exec(context.Background(), "run")
	if r.code != cli.ExitPartial || !strings.Contains(r.stderr, "1 module(s) failed to collect: probe") || !strings.Contains(r.stderr, "probe unavailable") {
		t.Fatalf("%+v", r)
	}
	id := h.entityID(t)
	if v := h.srv.EntityValues(id); v["hostname"] != "edge-fra-01" || v["probe_model"] != nil {
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
		if h.probeCalls.Load() != 0 || len(h.srv.Entities()) != 0 || len(h.writes("POST", "/")) != 0 {
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
	for _, want := range []string{"+ template host", "(dry-run: schema not applied)", "would publish to (entity to be created): 3 dimensions, 1 observations",
		`hostname.hostname → hostname = "edge-fra-01" @ 2026-09-22T10:00:00Z`, `probe.usage → probe_usage_pct ← "1" @`,
		`probe.arch → probe_arch = "arm64" @ 2026-09-22T10:00:00Z (option created by schema apply)`} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout should contain %q:\n%s", want, r.stdout)
		}
	}
	// Amended 2026-09-25 (FR-021): a list option the schema plan would create is
	// shown as what would be published, not dropped with an error.
	if strings.Contains(r.stderr, "observation dropped") || strings.Contains(r.stderr, "dropped=1") {
		t.Errorf("a planned list option must not be reported as dropped:\n%s", r.stderr)
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
	h := newHarness(t, "modules:\n  probe:\n    interval: 15s\npublish:\n  interval: 45s\n")
	r := h.exec(context.Background(), "run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{`msg="module scheduled" module=hostname interval=5m0s`, `msg="module scheduled" module=probe interval=15s`,
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

// gatedHarness adds a module whose "temp" attribute is collectable on linux
// only, and whose "usage" is collectable everywhere (spec 004 FR-018's shape).
func gatedHarness(t *testing.T, cfg, goos string) *harness {
	t.Helper()
	h := newHarness(t, cfg)
	h.goos = goos
	m := module.Static{M: manifest.Manifest{Module: "sensor", Attributes: []manifest.Attribute{
		{Key: "usage", Name: "Sensor usage", Slug: "sensor_usage_pct", Kind: manifest.KindMetric},
		{Key: "temp", Name: "Sensor temperature", Slug: "sensor_temp_c", Kind: manifest.KindMetric,
			Platforms: []string{"linux"}},
	}}}
	h.reg.Register(moduletest.WithProvider(m, 10*time.Second, func(context.Context) ([]module.Observation, error) {
		// A well-behaved provider does not offer what the platform cannot
		// report; this one offers both, so the core's safety net is exercised.
		return []module.Observation{{Key: "usage", Value: 5.0}, {Key: "temp", Value: 42.0}}, nil
	}))
	return h
}

// FR-021/FR-023: what this platform cannot collect is said once at startup,
// and an observation for it never becomes a per-tick error (FR-016).
func TestRun_UncollectableAttributeIsReportedOnceNotPerTick(t *testing.T) {
	h := gatedHarness(t, "", "windows")
	r := h.exec(context.Background(), "run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	logs := r.stderr
	if n := strings.Count(logs, "attribute skipped: not collectable on this platform"); n != 1 {
		t.Fatalf("skip must be reported exactly once, got %d:\n%s", n, logs)
	}
	for _, want := range []string{"module=sensor", "key=temp", "slug=sensor_temp_c", "platform=windows", "collectable_on=linux"} {
		if !strings.Contains(logs, want) {
			t.Errorf("skip record should contain %q:\n%s", want, logs)
		}
	}
	// FR-016: the provider offered `temp` anyway — that is not an omission and
	// must not be logged as an error on every collection.
	if strings.Contains(logs, "key not declared in manifest") {
		t.Errorf("an uncollectable key must not be reported as undeclared:\n%s", logs)
	}
	// FR-020: the schema still carries it, whatever the platform.
	if !strings.Contains(r.stdout, "schema reconciled") {
		t.Fatalf("stdout: %s", r.stdout)
	}
	vals := h.srv.EntityMetrics(h.entityID(t))
	if _, present := vals["sensor_temp_c"]; present {
		t.Error("an uncollectable attribute must never be published")
	}
	if got := vals["sensor_usage_pct"]; len(got) != 1 {
		t.Errorf("the collectable attribute must still be published: %v", got)
	}
}

// FR-020: gating changes what is collected, never what is declared. The same
// schema plan comes out of a windows host and a linux one.
func TestRun_SchemaIsTheSameOnEveryPlatform(t *testing.T) {
	plan := func(goos string) string {
		h := gatedHarness(t, "", goos)
		r := h.exec(context.Background(), "run", "--dry-run")
		if r.code != 0 {
			t.Fatalf("%s: %+v", goos, r)
		}
		return r.stdout
	}
	win, lin := plan("windows"), plan("linux")
	for _, want := range []string{"+ attribute sensor_temp_c (metric)", "+ attribute sensor_usage_pct (metric)"} {
		if !strings.Contains(win, want) || !strings.Contains(lin, want) {
			t.Errorf("both platforms must plan %q", want)
		}
	}
}

// FR-021/FR-022: "disabled" and "not collectable here" are different states.
// Disabling removes the schema too; the platform gate never does, and enabling
// a module the platform cannot collect skips rather than fails.
func TestRun_DisabledIsNotTheSameAsUncollectable(t *testing.T) {
	// Disabled: no schema, no values, absent from the schedule.
	h := gatedHarness(t, "modules:\n  sensor:\n    enabled: false\n", "linux")
	r := h.exec(context.Background(), "run", "--dry-run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.stdout, "sensor_temp_c") || strings.Contains(r.stderr, "module scheduled\" module=sensor") {
		t.Errorf("a disabled module contributes nothing:\n%s%s", r.stdout, r.stderr)
	}
	if strings.Contains(r.stderr, "skipped: not collectable") {
		t.Errorf("a disabled module is not a platform skip:\n%s", r.stderr)
	}

	// Explicitly enabled but uncollectable: skipped, not an error (FR-022).
	h2 := gatedHarness(t, "modules:\n  sensor:\n    enabled: true\n    interval: 5s\n", "windows")
	r2 := h2.exec(context.Background(), "run")
	if r2.code != 0 {
		t.Fatalf("enabling an uncollectable attribute must not fail the run: %+v", r2)
	}
	if !strings.Contains(r2.stderr, "attribute skipped") {
		t.Errorf("expected a skip record:\n%s", r2.stderr)
	}
}

// US-5/3: the dry-run explains an absent value instead of leaving the operator
// to guess — as text once, and inside every JSON document for a script.
func TestRun_DryRunShowsWhatThePlatformCannotCollect(t *testing.T) {
	h := gatedHarness(t, "", "windows")
	r := h.exec(context.Background(), "run", "--dry-run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stdout, "not collectable on windows:") ||
		!strings.Contains(r.stdout, "sensor.temp → sensor_temp_c (collectable on linux)") {
		t.Fatalf("dry-run should explain the gap:\n%s", r.stdout)
	}

	h2 := gatedHarness(t, "", "windows")
	r2 := h2.exec(context.Background(), "run", "--dry-run", "--json")
	if r2.code != 0 {
		t.Fatalf("%+v", r2)
	}
	var doc struct {
		Version int `json:"version"`
		Skipped []struct {
			Module, Key, Slug string
			Platforms         []string
		} `json:"skipped"`
	}
	line := strings.TrimSpace(lastJSONLine(r2.stdout))
	if err := json.Unmarshal([]byte(line), &doc); err != nil {
		t.Fatalf("dry-run JSON: %v\n%s", err, r2.stdout)
	}
	if doc.Version != 1 || len(doc.Skipped) != 1 || doc.Skipped[0].Slug != "sensor_temp_c" ||
		strings.Join(doc.Skipped[0].Platforms, ",") != "linux" {
		t.Fatalf("skipped in JSON: %+v", doc)
	}
	// The text block is for humans; JSON callers get it in the document.
	if strings.Contains(r2.stdout, "not collectable on windows:") {
		t.Errorf("JSON mode must not emit the text block:\n%s", r2.stdout)
	}
}

// lastJSONLine returns the last line of out that parses as a JSON object.
func lastJSONLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "{") {
			return lines[i]
		}
	}
	return ""
}

// FR-024 (003 FR-026): collected values are logged at debug only. An operator
// running at info must never see a host's readings in the log, and no record
// may carry the token or the project id.
func TestRun_ValuesAreDebugOnly(t *testing.T) {
	h := gatedHarness(t, "", "linux")
	r := h.exec(context.Background(), "run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, line := range strings.Split(r.stderr, "\n") {
		if line == "" || strings.Contains(line, "level=DEBUG") {
			continue
		}
		for _, secret := range []string{"edge-fra-01", omnitest.Token, omnitest.ProjectID} {
			if strings.Contains(line, secret) {
				t.Errorf("value or credential above debug level: %s", line)
			}
		}
	}
}

// NFR-003: the publish request count depends on the publish interval and the
// number of observations, never on how many modules or attributes there are.
// Adding cpu-shaped modules must not add requests.
func TestRun_RequestCountIsIndependentOfModules(t *testing.T) {
	h := gatedHarness(t, "", "linux")
	before := len(h.srv.Requests())
	r := h.exec(context.Background(), "run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	writes := 0
	for _, req := range h.srv.Requests()[before:] {
		if strings.HasPrefix(req.Path, "/entities/") && (req.Method == "PATCH" || strings.HasSuffix(req.Path, "/metrics")) {
			writes++
		}
	}
	// One dimension update plus one metric chunk, whatever the module count.
	if writes != 2 {
		t.Fatalf("a publish must cost 1 + ceil(n/1000) requests, got %d writes", writes)
	}
}
