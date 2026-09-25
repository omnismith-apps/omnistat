package cli_test

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

// daemon starts `run --daemon` in the background and returns a way to stop it
// and read its result.
type daemonRun struct {
	cancel context.CancelFunc
	done   chan run
}

func (h *harness) daemon(ctx context.Context, args ...string) *daemonRun {
	ctx, cancel := context.WithCancel(ctx)
	d := &daemonRun{cancel: cancel, done: make(chan run, 1)}
	go func() {
		var out syncBuffer
		start := len(h.logs.String())
		code := h.app().Run(ctx, append([]string{"--config", h.cfg, "--log-level", "debug", "run", "--daemon"}, args...), &out, &h.logs, func(k string) string { return h.env[k] })
		d.done <- run{code, out.String(), h.logs.String()[start:]}
	}()
	return d
}

func (d *daemonRun) stop(t *testing.T) run {
	t.Helper()
	d.cancel()
	select {
	case r := <-d.done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
		return run{}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Microsecond)
	}
}

// parked waits until the two sources (hostname, cpu) and the publish loop are
// all asleep, i.e. the daemon has nothing in flight.
func (h *harness) parked(t *testing.T) {
	t.Helper()
	waitFor(t, "daemon parked", func() bool { return h.clock.Sleepers() == 3 })
}

func count(reqs []omnitest.Request) int { return len(reqs) }

// US-2/1…3, FR-014, FR-019: reconcile/resolve once; first publish after the first
// round; then on the grid; nothing sent when nothing was collected; stamps are
// collection times. The publish grid (55s) is chosen not to coincide with the
// cpu grid (10s): when both fire at the same instant the order is arbitrary and
// the sample simply lands on the next publish.
func TestDaemon_Schedule(t *testing.T) {
	h := newHarness(t, "modules:\n  probe:\n    interval: 10s\npublish:\n  interval: 55s\n")
	d := h.daemon(context.Background())
	// Two sources (hostname 5m, cpu 10s) + the publish loop.
	h.parked(t)
	id := h.entityID(t)
	if n := count(h.writes("PATCH", "/entities/")); n != 1 {
		t.Fatalf("first publish right after the first round: %d PATCH", n)
	}
	if m := h.srv.EntityMetrics(id)["probe_usage_pct"]; len(m) != 1 {
		t.Fatalf("first metrics: %+v", m)
	}
	schemaWrites, creates := count(h.writes("POST", "/templates"))+count(h.writes("POST", "/attributes")), count(h.writes("POST", "/entities/template/"))

	// t=50: five more cpu samples collected, nothing published yet.
	for i := 0; i < 5; i++ {
		h.clock.Advance(10 * time.Second)
		h.parked(t)
	}
	if n := count(h.writes("PATCH", "/entities/")); n != 1 {
		t.Fatalf("no publish before the grid tick: %d", n)
	}
	// t=55: second publish carries them with their own stamps.
	h.clock.Advance(5 * time.Second)
	waitFor(t, "second publish", func() bool { return count(h.writes("PATCH", "/entities/")) == 2 })
	h.parked(t)
	m := h.srv.EntityMetrics(id)["probe_usage_pct"]
	if len(m) != 6 || m[1].UpdatedAt != "2026-09-22T10:00:10Z" || m[5].UpdatedAt != "2026-09-22T10:00:50Z" {
		t.Fatalf("metrics after 55s: %d %+v", len(m), m)
	}
	if n := count(h.writes("POST", "/entities/"+id+"/metrics")); n != 2 {
		t.Fatalf("metric requests: %d", n)
	}
	// Hostname (5m) was not re-collected, so the second PATCH carried probe_model only.
	patches := h.writes("PATCH", "/entities/")
	if strings.Contains(patches[1].Body, "hostname") || !strings.Contains(patches[1].Body, "probe_model") {
		t.Fatalf("second PATCH: %s", patches[1].Body)
	}
	if count(h.writes("POST", "/templates"))+count(h.writes("POST", "/attributes")) != schemaWrites || count(h.writes("POST", "/entities/template/")) != creates {
		t.Fatal("daemon must not re-reconcile or re-resolve")
	}

	// US-2/3: cpu fails from now on → the tick at 110 finds nothing → no request.
	// (The fake clock must land exactly on each deadline: a goroutine stamps
	// with the clock as it is when it wakes.)
	h.probeFail.Store(true)
	before := len(h.srv.Requests())
	h.clock.Advance(5 * time.Second) // 60
	h.parked(t)
	for i := 0; i < 5; i++ {
		h.clock.Advance(10 * time.Second) // 70 … 110
		h.parked(t)
	}
	if len(h.srv.Requests()) != before {
		t.Fatalf("expected no request, got %d new", len(h.srv.Requests())-before)
	}
	if !strings.Contains(h.logsSnapshot(), "msg=\"nothing to publish\"") {
		t.Fatal("expected the empty tick to be logged at debug")
	}

	// FR-020: one more sample at 120, then stop → final publish → exit 0.
	h.probeFail.Store(false)
	h.clock.Advance(10 * time.Second)
	h.parked(t)
	r := d.stop(t)
	if r.code != 0 || !strings.Contains(r.stderr, "msg=stopped") {
		t.Fatalf("stop: %+v", r)
	}
	if got := h.srv.EntityMetrics(id)["probe_usage_pct"]; len(got) != 7 || got[6].UpdatedAt != "2026-09-22T10:02:00Z" {
		t.Fatalf("final publish: %+v", got)
	}
	if strings.Count(r.stderr, `msg="publish scheduled"`) != 1 || !strings.Contains(r.stderr, "daemon=true") {
		t.Fatalf("schedule log: %s", r.stderr)
	}
}

// US-3/1: the API is down for a while; the buffer is kept and backfilled with
// original timestamps when it recovers. (33s publish grid: never coincides
// with the 10s cpu grid within the test horizon.)
func TestDaemon_Outage(t *testing.T) {
	h := newHarness(t, "modules:\n  probe:\n    interval: 10s\npublish:\n  interval: 33s\nhttp:\n  retries: 0\n")
	d := h.daemon(context.Background())
	h.parked(t)
	id := h.entityID(t)

	// The API answers 503 for three publish ticks (33, 66, 99); the dimension
	// write goes first, so nothing else is attempted either.
	h.srv.FailNext(omnitest.Fault{Method: "PATCH", Status: 503, Times: 3})
	for i := 0; i < 10; i++ {
		h.clock.Advance(10 * time.Second) // 10 … 100
		h.parked(t)
	}
	if n := strings.Count(h.logsSnapshot(), "publish failed; keeping the buffer"); n != 3 {
		t.Fatalf("failed publishes: %d", n)
	}
	if got := len(h.srv.EntityMetrics(id)["probe_usage_pct"]); got != 1 {
		t.Fatalf("nothing should have landed during the outage, got %d", got)
	}
	// Recovery at 132: all 13 buffered observations (10 … 130) land with their
	// stamps. Advance exactly onto each event so the fake clock stays deterministic.
	for i := 0; i < 3; i++ {
		h.clock.Advance(10 * time.Second) // 110 … 130
		h.parked(t)
	}
	h.clock.Advance(2 * time.Second) // 132
	h.parked(t)
	m := h.srv.EntityMetrics(id)["probe_usage_pct"]
	if len(m) != 14 || m[1].UpdatedAt != "2026-09-22T10:00:10Z" || m[13].UpdatedAt != "2026-09-22T10:02:10Z" {
		t.Fatalf("backfill: %d %+v", len(m), m)
	}
	d.stop(t)
}

// US-3/2, FR-008: the bound is reached during an outage → oldest dropped,
// logged once per publish interval, memory bounded. The bound itself (5 000)
// is unit-tested in collect; here it is lowered to keep the test fast.
func TestDaemon_BufferBound(t *testing.T) {
	h := newHarness(t, "modules:\n  probe:\n    interval: 1s\n  hostname:\n    interval: 24h\npublish:\n  interval: 30s\nhttp:\n  retries: 0\n")
	h.maxPerMetric = 20
	d := h.daemon(context.Background())
	h.parked(t)
	id := h.entityID(t)
	h.srv.FailNext(omnitest.Fault{Method: "PATCH", Status: 503, Times: 100})
	for i := 0; i < 60; i++ {
		h.clock.Advance(time.Second)
		h.parked(t)
	}
	// 60 samples collected, 20 kept: 39–40 dropped (the 1s and 30s grids
	// coincide at the ticks, so one sample may land on either side), reported
	// once at each of the ticks at 30s and 60s.
	logs := h.logsSnapshot()
	if n := strings.Count(logs, "metric buffer full"); n != 2 || !strings.Contains(logs, "slug=probe_usage_pct") {
		t.Fatalf("drop warnings: %d\n%s", n, logs)
	}
	total := 0
	for _, m := range regexp.MustCompile(`dropped=(\d+)\n`).FindAllStringSubmatch(logs, -1) {
		n, _ := strconv.Atoi(m[1])
		total += n
	}
	if total < 39 || total > 40 {
		t.Fatalf("drops reported: %d\n%s", total, logs)
	}
	d.stop(t)
	if got := h.srv.EntityMetrics(id)["probe_usage_pct"]; len(got) != 1 {
		t.Fatalf("outage: %d", len(got))
	}
}

// US-3/3, FR-015: the entity disappears → exit non-zero naming it.
func TestDaemon_EntityGone(t *testing.T) {
	h := newHarness(t, "modules:\n  probe:\n    interval: 10s\npublish:\n  interval: 30s\n")
	d := h.daemon(context.Background())
	h.parked(t)
	id := h.entityID(t)
	h.srv.FailNext(omnitest.Fault{Method: "PATCH", Status: 404, Times: 1})
	for i := 0; i < 3; i++ {
		h.clock.Advance(10 * time.Second)
		if i < 2 {
			h.parked(t)
		}
	}
	r := <-d.done
	if r.code != 1 || !strings.Contains(r.stderr, "host entity no longer exists ("+id+")") {
		t.Fatalf("%+v", r)
	}
}

// FR-014: a publish slower than the interval coalesces the missed ticks.
func TestDaemon_SlowPublish(t *testing.T) {
	h := newHarness(t, "modules:\n  probe:\n    interval: 10s\npublish:\n  interval: 30s\n")
	// Every PATCH takes 70 seconds of fake time.
	h.srv.Before = func(r *http.Request) {
		if r.Method == "PATCH" {
			h.clock.Advance(70 * time.Second)
		}
	}
	d := h.daemon(context.Background())
	h.parked(t)
	// First publish ran 0→70s; the grid is 0, 30, 60, 90 … so the next tick is 90.
	if now := h.clock.Now(); !now.Equal(t0.Add(70 * time.Second)) {
		t.Fatalf("clock after first publish: %v", now)
	}
	patches := count(h.writes("PATCH", "/entities/"))
	h.clock.Advance(20 * time.Second) // 90: tick → publish 90→160
	waitFor(t, "second publish", func() bool { return count(h.writes("PATCH", "/entities/")) == patches+1 })
	h.parked(t)
	if now := h.clock.Now(); !now.Equal(t0.Add(160 * time.Second)) {
		t.Fatalf("clock after second publish: %v", now)
	}
	// Ticks 120 and 150 were missed and are not replayed; the next is 180.
	h.clock.Advance(19 * time.Second)
	h.parked(t)
	if count(h.writes("PATCH", "/entities/")) != patches+1 {
		t.Fatal("published before the next grid tick")
	}
	h.clock.Advance(time.Second)
	waitFor(t, "grid tick", func() bool { return count(h.writes("PATCH", "/entities/")) == patches+2 })
	d.stop(t)
}

// US-4/2: daemon + dry-run prints every tick and never writes.
func TestDaemon_DryRun(t *testing.T) {
	h := newHarness(t, "modules:\n  probe:\n    interval: 10s\npublish:\n  interval: 30s\n")
	d := h.daemon(context.Background(), "--dry-run")
	h.parked(t)
	for i := 0; i < 3; i++ {
		h.clock.Advance(10 * time.Second)
		h.parked(t)
	}
	r := d.stop(t)
	if r.code != 0 || strings.Count(r.stdout, "would publish to (entity to be created)") < 2 {
		t.Fatalf("%+v", r)
	}
	for _, q := range h.srv.Requests() {
		if q.Method != "GET" {
			t.Fatalf("dry-run wrote: %+v", q)
		}
	}
}

// NFR-005: the final publish is bounded by the HTTP timeout.
func TestDaemon_FinalPublishBounded(t *testing.T) {
	h := newHarness(t, "publish:\n  interval: 30s\nhttp:\n  timeout: 300ms\n  retries: 0\n")
	d := h.daemon(context.Background())
	h.parked(t)
	// Make the server stall on the final PATCH.
	stall := make(chan struct{})
	h.srv.Before = func(r *http.Request) {
		if r.Method == "PATCH" {
			<-stall
		}
	}
	h.host.Store("renamed")
	h.clock.Advance(5 * time.Minute) // hostname re-collected → something to flush
	h.parked(t)
	started := time.Now()
	r := d.stop(t)
	close(stall)
	if r.code != 0 || time.Since(started) > 2*time.Second || !strings.Contains(r.stderr, "final publish failed") {
		t.Fatalf("%+v after %s", r, time.Since(started))
	}
}

// Spec 003 FR-026a: in daemon mode, the first publish is logged at info, later
// ones at debug; a summary every log.summary_interval; a failure at once, the
// recovery at info; the unfinished period's summary on stop, before "stopped".
func TestDaemon_LogSummary(t *testing.T) {
	h := newHarness(t, "modules:\n  probe:\n    interval: 20s\npublish:\n  interval: 1m\nhttp:\n  retries: 0\nlog:\n  summary_interval: 3m\n")
	d := h.daemon(context.Background())
	h.parked(t)
	infoPublished := func() int { return strings.Count(h.logsSnapshot(), "level=INFO msg=published ") }
	if n := infoPublished(); n != 1 {
		t.Fatalf("first publish at info: %d\n%s", n, h.logsSnapshot())
	}
	advance := func(steps int) { // 20s steps land on both the probe and the publish grid
		for i := 0; i < steps; i++ {
			h.clock.Advance(20 * time.Second)
			h.parked(t)
		}
	}
	advance(6) // t=120: publishes at 60 and 120, both at debug
	if n := infoPublished(); n != 1 || strings.Count(h.logsSnapshot(), "level=DEBUG msg=published ") != 2 {
		t.Fatalf("later publishes at debug:\n%s", h.logsSnapshot())
	}
	if strings.Contains(h.logsSnapshot(), "publish summary") {
		t.Fatal("no summary before the interval")
	}
	advance(3) // t=180: 4th publish, then the 3m summary
	if !strings.Contains(h.logsSnapshot(), `level=INFO msg="publish summary" period=3m0s publishes=4 `) || !strings.Contains(h.logsSnapshot(), " failed=0 ") {
		t.Fatalf("summary:\n%s", h.logsSnapshot())
	}

	h.srv.FailNext(omnitest.Fault{Method: "PATCH", Status: 503, Times: 1})
	advance(3) // t=240: fails
	if !strings.Contains(h.logsSnapshot(), "level=ERROR msg=\"publish failed\"") {
		t.Fatalf("failure at once:\n%s", h.logsSnapshot())
	}
	advance(3) // t=300: recovers
	if !strings.Contains(h.logsSnapshot(), `level=INFO msg="publish recovered"`) || !strings.Contains(h.logsSnapshot(), "after_failures=1") {
		t.Fatalf("recovery at info:\n%s", h.logsSnapshot())
	}

	r := d.stop(t)
	final := strings.LastIndex(r.stderr, `msg="publish summary"`)
	stopped := strings.LastIndex(r.stderr, "msg=stopped")
	if r.code != 0 || final < 0 || stopped < final || !strings.Contains(r.stderr[final:], "failed=1") {
		t.Fatalf("final summary before stopped, counting the failure:\n%s", r.stderr)
	}
}

// FR-026a: log.summary_interval: 0 logs every publish at info, with no summaries.
func TestDaemon_LogEveryPublish(t *testing.T) {
	h := newHarness(t, "modules:\n  probe:\n    interval: 20s\npublish:\n  interval: 1m\nlog:\n  summary_interval: 0\n")
	d := h.daemon(context.Background())
	h.parked(t)
	for i := 0; i < 6; i++ {
		h.clock.Advance(20 * time.Second)
		h.parked(t)
	}
	r := d.stop(t)
	// Publishes at 0, 60 and 120; the final one on stop may find an empty buffer.
	if n := strings.Count(r.stderr, "level=INFO msg=published "); n < 3 || strings.Contains(r.stderr, "publish summary") || strings.Contains(r.stderr, "level=DEBUG msg=published ") {
		t.Fatalf("every publish at info: %d\n%s", n, r.stderr)
	}
}
