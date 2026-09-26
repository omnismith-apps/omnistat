package collect_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/moduletest"
)

var t0 = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

// desiredFor resolves the fixture manifests with no overrides.
func desiredFor(t *testing.T, mods ...module.Module) manifest.Desired {
	t.Helper()
	d, err := manifest.Resolve(module.Manifests(mods), manifest.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// counter is a scripted provider that counts calls.
type counter struct {
	calls   atomic.Int32
	inside  atomic.Int32
	maxIn   atomic.Int32
	collect func(ctx context.Context, n int32) ([]module.Observation, error)
}

func (c *counter) fn(ctx context.Context) ([]module.Observation, error) {
	n := c.calls.Add(1)
	in := c.inside.Add(1)
	for {
		m := c.maxIn.Load()
		if in <= m || c.maxIn.CompareAndSwap(m, in) {
			break
		}
	}
	defer c.inside.Add(-1)
	return c.collect(ctx, n)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func start(t *testing.T, sources []collect.Source, clock collect.Clock, logw *bytes.Buffer) (*collect.Scheduler, *collect.Buffer, context.CancelFunc) {
	t.Helper()
	buf := collect.NewBuffer(10)
	var log *slog.Logger
	if logw != nil {
		log = slog.New(slog.NewTextHandler(logw, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	s := collect.NewScheduler(sources, buf, clock, log)
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	t.Cleanup(func() { cancel(); <-s.Done() })
	return s, buf, cancel
}

// FR-003/004/014: each source on its own cadence, first round signalled, stamps at return.
func TestScheduler_Cadence(t *testing.T) {
	cpu := &counter{collect: func(context.Context, int32) ([]module.Observation, error) {
		return []module.Observation{{Key: "usage", Value: 42}}, nil
	}}
	vol := &counter{collect: func(context.Context, int32) ([]module.Observation, error) {
		return []module.Observation{{Key: "count", Value: 2}}, nil
	}}
	cpuMod := moduletest.WithProvider(moduletest.Probe(), 10*time.Second, cpu.fn)
	volMod := moduletest.WithProvider(moduletest.Volume(), 30*time.Second, vol.fn)
	sources, _, err := collect.Sources(desiredFor(t, cpuMod, volMod), []module.Module{cpuMod, volMod}, nil, "linux")
	if err != nil {
		t.Fatal(err)
	}
	clock := collect.NewFakeClock(t0)
	s, buf, _ := start(t, sources, clock, nil)

	<-s.FirstRound()
	if cpu.calls.Load() != 1 || vol.calls.Load() != 1 {
		t.Fatalf("first round: cpu %d volume %d", cpu.calls.Load(), vol.calls.Load())
	}
	b := buf.Snapshot()
	if len(b.Dims) != 1 || b.Dims[0].Slug != "volume_count" || b.Dims[0].Value != 2.0 || !b.Dims[0].At.Equal(t0) {
		t.Fatalf("dims: %+v", b.Dims)
	}
	if len(b.Metrics) != 1 || b.Metrics[0].Slug != "probe_usage_pct" || b.Metrics[0].Value != 42.0 || !b.Metrics[0].At.Equal(t0) {
		t.Fatalf("metrics: %+v", b.Metrics)
	}

	waitFor(t, "both parked", func() bool { return clock.Sleepers() == 2 })
	clock.Advance(10 * time.Second)
	waitFor(t, "cpu 2nd call", func() bool { return cpu.calls.Load() == 2 })
	if vol.calls.Load() != 1 {
		t.Fatalf("volume must not have run: %d", vol.calls.Load())
	}
	waitFor(t, "cpu parked", func() bool { return clock.Sleepers() == 2 })
	clock.Advance(20 * time.Second)
	waitFor(t, "cpu 3rd, volume 2nd", func() bool { return cpu.calls.Load() == 3 && vol.calls.Load() == 2 })
	waitFor(t, "parked", func() bool { return clock.Sleepers() == 2 })
	b = buf.Snapshot()
	if len(b.Metrics) != 3 || !b.Metrics[2].At.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("stamps: %+v", b.Metrics)
	}
}

// FR-006: the stamp is the clock when the provider returned, not when it was called.
func TestScheduler_StampAtReturn(t *testing.T) {
	clock := collect.NewFakeClock(t0)
	m := moduletest.WithProvider(moduletest.Probe(), time.Minute, func(context.Context) ([]module.Observation, error) {
		clock.Advance(3 * time.Second)
		return []module.Observation{{Key: "model", Value: "x"}}, nil
	})
	sources, _, _ := collect.Sources(desiredFor(t, m), []module.Module{m}, nil, "linux")
	s, buf, _ := start(t, sources, clock, nil)
	<-s.FirstRound()
	if b := buf.Snapshot(); !b.Dims[0].At.Equal(t0.Add(3 * time.Second)) {
		t.Fatalf("stamp: %v", b.Dims[0].At)
	}
}

// FR-010: a failing or panicking module is isolated and retried; FR-002: bad observations dropped.
func TestScheduler_FailureIsolation(t *testing.T) {
	bad := &counter{collect: func(_ context.Context, n int32) ([]module.Observation, error) {
		if n%2 == 1 {
			return nil, errors.New("boom")
		}
		panic("kaboom")
	}}
	good := &counter{collect: func(context.Context, int32) ([]module.Observation, error) {
		return []module.Observation{
			{Key: "count", Value: 1},
			{Key: "nope", Value: 1},
			{Key: "mount", Value: 12},
		}, nil
	}}
	badMod := moduletest.WithProvider(moduletest.Probe(), 10*time.Second, bad.fn)
	goodMod := moduletest.WithProvider(moduletest.Volume(), 10*time.Second, good.fn)
	sources, _, _ := collect.Sources(desiredFor(t, badMod, goodMod), []module.Module{badMod, goodMod}, nil, "linux")
	clock := collect.NewFakeClock(t0)
	var logw bytes.Buffer
	s, buf, _ := start(t, sources, clock, &logw)
	<-s.FirstRound()
	waitFor(t, "parked", func() bool { return clock.Sleepers() == 2 })
	clock.Advance(10 * time.Second)
	waitFor(t, "second round", func() bool { return bad.calls.Load() == 2 && good.calls.Load() == 2 })
	waitFor(t, "parked", func() bool { return clock.Sleepers() == 2 })

	b := buf.Snapshot()
	if len(b.Dims) != 1 || b.Dims[0].Slug != "volume_count" || len(b.Metrics) != 0 {
		t.Fatalf("buffer: %+v", b)
	}
	logs := logw.String()
	for _, want := range []string{`collection failed`, `module=probe`, `error=boom`, `provider panicked: kaboom`,
		`key not declared in manifest`, `key=nope`, `invalid value`, `key=mount`, `module=volume`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log should contain %q:\n%s", want, logs)
		}
	}
}

// FR-004: a provider slower than its interval is cancelled and called again at once.
func TestScheduler_Deadline(t *testing.T) {
	slow := &counter{collect: func(ctx context.Context, _ int32) ([]module.Observation, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	m := moduletest.WithProvider(moduletest.Probe(), 10*time.Second, slow.fn)
	sources, _, _ := collect.Sources(desiredFor(t, m), []module.Module{m}, nil, "linux")
	clock := collect.NewFakeClock(t0)
	s, buf, cancel := start(t, sources, clock, nil)
	waitFor(t, "provider blocked", func() bool { return slow.inside.Load() == 1 })
	clock.Advance(10 * time.Second)
	<-s.FirstRound()
	waitFor(t, "called again", func() bool { return slow.calls.Load() == 2 })
	if !buf.Empty() {
		t.Fatal("nothing should be buffered")
	}
	cancel()
	<-s.Done()
}

// FR-011: no overlap within a module, concurrency across modules; NFR-002: no leak.
func TestScheduler_Concurrency(t *testing.T) {
	var gate sync.WaitGroup
	gate.Add(2) // both providers must be inside at the same time, or this deadlocks
	mk := func() *counter {
		return &counter{collect: func(ctx context.Context, _ int32) ([]module.Observation, error) {
			gate.Done()
			gate.Wait()
			return nil, nil
		}}
	}
	a, b := mk(), mk()
	am := moduletest.WithProvider(moduletest.Probe(), time.Second, a.fn)
	bm := moduletest.WithProvider(moduletest.Volume(), time.Second, b.fn)
	sources, _, _ := collect.Sources(desiredFor(t, am, bm), []module.Module{am, bm}, nil, "linux")
	clock := collect.NewFakeClock(t0)
	before := runtime.NumGoroutine()
	s, _, cancel := start(t, sources, clock, nil)
	<-s.FirstRound()
	if a.maxIn.Load() != 1 || b.maxIn.Load() != 1 {
		t.Fatalf("overlap within a module: a %d b %d", a.maxIn.Load(), b.maxIn.Load())
	}
	cancel()
	<-s.Done()
	waitFor(t, "goroutines released", func() bool { return runtime.NumGoroutine() <= before })
}

// FR-018: Once runs every source once and names the failures.
func TestOnce(t *testing.T) {
	ok := moduletest.WithProvider(moduletest.Volume(), time.Minute, func(context.Context) ([]module.Observation, error) {
		return []module.Observation{{Key: "count", Value: 3}}, nil
	})
	bad := moduletest.WithProvider(moduletest.Probe(), time.Minute, func(context.Context) ([]module.Observation, error) {
		return nil, errors.New("no probe today")
	})
	sources, _, _ := collect.Sources(desiredFor(t, ok, bad), []module.Module{ok, bad}, nil, "linux")
	buf := collect.NewBuffer(0)
	failed := collect.Once(context.Background(), sources, buf, collect.NewFakeClock(t0), nil)
	if strings.Join(failed, ",") != "probe" {
		t.Fatalf("failed: %v", failed)
	}
	if b := buf.Snapshot(); len(b.Dims) != 1 || b.Dims[0].Value != 3.0 || !b.Dims[0].At.Equal(t0) {
		t.Fatalf("buffer: %+v", b)
	}
}

// FR-001/003: Sources skips modules without a provider, applies overrides,
// rejects an interval for a module that produces nothing.
func TestSources(t *testing.T) {
	probe := moduletest.WithProvider(moduletest.Probe(), 10*time.Second, nil)
	ident := moduletest.Ident()
	mods := []module.Module{ident, probe}
	sources, _, err := collect.Sources(desiredFor(t, mods...), mods, map[string]time.Duration{"probe": 5 * time.Second}, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Module != "probe" || sources[0].Interval != 5*time.Second || len(sources[0].Attrs) != 3 || sources[0].Attrs["usage"].Slug != "probe_usage_pct" {
		t.Fatalf("sources: %+v", sources)
	}
	sources, _, err = collect.Sources(desiredFor(t, mods...), mods, nil, "linux")
	if err != nil || sources[0].Interval != 10*time.Second {
		t.Fatalf("default interval: %+v %v", sources, err)
	}
	_, _, err = collect.Sources(desiredFor(t, mods...), mods, map[string]time.Duration{"ident": time.Minute}, "linux")
	if err == nil || !strings.Contains(err.Error(), "modules.ident.interval: module ident produces no values") {
		t.Fatalf("err: %v", err)
	}
}

// Overrides flow into sources: a remapped slug is what the sample carries.
func TestSources_Overrides(t *testing.T) {
	probe := moduletest.WithProvider(moduletest.Probe(), 10*time.Second, func(context.Context) ([]module.Observation, error) {
		return []module.Observation{{Key: "usage", Value: 1}}, nil
	})
	d, err := manifest.Resolve(module.Manifests([]module.Module{probe}), manifest.Overrides{Modules: map[string]manifest.ModuleOverride{
		"probe": {Attributes: map[string]manifest.AttributeOverride{"usage": {Slug: "probe_load"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	sources, _, _ := collect.Sources(d, []module.Module{probe}, nil, "linux")
	buf := collect.NewBuffer(0)
	collect.Once(context.Background(), sources, buf, collect.NewFakeClock(t0), nil)
	if b := buf.Snapshot(); len(b.Metrics) != 1 || b.Metrics[0].Slug != "probe_load" || b.Metrics[0].Key != "usage" {
		t.Fatalf("buffer: %+v", b)
	}
}

// gated builds a module whose "usage" is collectable everywhere and whose
// "load1" is collectable only on linux and darwin — the real shape of `cpu`
// (004 FR-018).
func gated(name string) module.Module {
	return module.Static{M: manifest.Manifest{Module: name, Attributes: []manifest.Attribute{
		{Key: "usage", Name: "Usage", Slug: name + "_usage_pct", Kind: manifest.KindMetric},
		{Key: "load1", Name: "Load 1m", Slug: name + "_load_avg_1", Kind: manifest.KindMetric,
			Platforms: []string{"linux", "darwin"}},
	}}}
}

// linuxOnly builds a module no attribute of which can be collected off linux.
func linuxOnly(name string) module.Module {
	return module.Static{M: manifest.Manifest{Module: name, Attributes: []manifest.Attribute{
		{Key: "temp", Name: "Temp", Slug: name + "_temp_c", Kind: manifest.KindMetric,
			Platforms: []string{"linux"}},
	}}}
}

// FR-018/FR-019 (ADR-0007): an attribute the platform cannot report is kept out
// of the source's Attrs and reported once as Skipped; a module with nothing left
// to collect is not scheduled at all. The desired schema is untouched either way
// (FR-020).
func TestSources_PlatformGating(t *testing.T) {
	g := moduletest.WithProvider(gated("gate"), 10*time.Second, nil)
	mods := []module.Module{g}
	desired := desiredFor(t, mods...)

	// On linux both attributes are collectable and nothing is skipped.
	src, skipped, err := collect.Sources(desired, mods, nil, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if len(src) != 1 || len(src[0].Attrs) != 2 || len(src[0].Unsupported) != 0 || len(skipped) != 0 {
		t.Fatalf("linux: attrs=%v unsupported=%v skipped=%v", src[0].Attrs, src[0].Unsupported, skipped)
	}

	// On windows the load average is dropped — it is reported once, not per tick.
	src, skipped, err = collect.Sources(desired, mods, nil, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if len(src) != 1 || len(src[0].Attrs) != 1 {
		t.Fatalf("windows: attrs=%v", src[0].Attrs)
	}
	if _, ok := src[0].Attrs["usage"]; !ok {
		t.Fatal("usage must stay collectable on windows")
	}
	if !src[0].Unsupported["load1"] {
		t.Fatalf("load1 must be marked unsupported: %v", src[0].Unsupported)
	}
	if len(skipped) != 1 || skipped[0].Module != "gate" || skipped[0].Key != "load1" ||
		skipped[0].Slug != "gate_load_avg_1" || strings.Join(skipped[0].Platforms, ",") != "linux,darwin" {
		t.Fatalf("skipped: %+v", skipped)
	}

	// FR-020: the schema does not depend on the platform.
	if len(desiredFor(t, mods...).Attributes) != 2 {
		t.Fatal("gating must not change the desired schema")
	}
}

// FR-019: nothing collectable here ⇒ the module is not scheduled, and it is
// reported as a whole rather than attribute by attribute.
func TestSources_WholeModuleSkipped(t *testing.T) {
	lo := moduletest.WithProvider(linuxOnly("therm"), 10*time.Second, nil)
	g := moduletest.WithProvider(gated("gate"), 10*time.Second, nil)
	mods := []module.Module{lo, g}

	src, skipped, err := collect.Sources(desiredFor(t, mods...), mods, nil, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if len(src) != 1 || src[0].Module != "gate" {
		t.Fatalf("therm must not be scheduled: %+v", src)
	}
	var whole *collect.Skipped
	for i := range skipped {
		if skipped[i].Module == "therm" {
			whole = &skipped[i]
		}
	}
	if whole == nil || whole.Key != "" || strings.Join(whole.Platforms, ",") != "linux" {
		t.Fatalf("module-level skip missing or wrong: %+v", skipped)
	}
}

// FR-022: the platform decides, not the config. Configuring an interval for a
// module that cannot collect here is a no-op, never an error — unlike an
// interval for a module that produces no values at all (FR-003).
func TestSources_IntervalForSkippedModuleIsNotAnError(t *testing.T) {
	lo := moduletest.WithProvider(linuxOnly("therm"), 10*time.Second, nil)
	mods := []module.Module{lo}
	src, skipped, err := collect.Sources(desiredFor(t, mods...), mods, map[string]time.Duration{"therm": 5 * time.Second}, "windows")
	if err != nil {
		t.Fatalf("enabling an unsupported module must skip, not fail: %v", err)
	}
	if len(src) != 0 || len(skipped) != 1 {
		t.Fatalf("src=%+v skipped=%+v", src, skipped)
	}
}
