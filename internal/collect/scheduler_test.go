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
	disk := &counter{collect: func(context.Context, int32) ([]module.Observation, error) {
		return []module.Observation{{Key: "count", Value: 2}}, nil
	}}
	cpuMod := moduletest.WithProvider(moduletest.CPU(), 10*time.Second, cpu.fn)
	diskMod := moduletest.WithProvider(moduletest.Disk(), 30*time.Second, disk.fn)
	sources, err := collect.Sources(desiredFor(t, cpuMod, diskMod), []module.Module{cpuMod, diskMod}, nil)
	if err != nil {
		t.Fatal(err)
	}
	clock := collect.NewFakeClock(t0)
	s, buf, _ := start(t, sources, clock, nil)

	<-s.FirstRound()
	if cpu.calls.Load() != 1 || disk.calls.Load() != 1 {
		t.Fatalf("first round: cpu %d disk %d", cpu.calls.Load(), disk.calls.Load())
	}
	b := buf.Snapshot()
	if len(b.Dims) != 1 || b.Dims[0].Slug != "disk_count" || b.Dims[0].Value != 2.0 || !b.Dims[0].At.Equal(t0) {
		t.Fatalf("dims: %+v", b.Dims)
	}
	if len(b.Metrics) != 1 || b.Metrics[0].Slug != "cpu_usage_pct" || b.Metrics[0].Value != 42.0 || !b.Metrics[0].At.Equal(t0) {
		t.Fatalf("metrics: %+v", b.Metrics)
	}

	waitFor(t, "both parked", func() bool { return clock.Sleepers() == 2 })
	clock.Advance(10 * time.Second)
	waitFor(t, "cpu 2nd call", func() bool { return cpu.calls.Load() == 2 })
	if disk.calls.Load() != 1 {
		t.Fatalf("disk must not have run: %d", disk.calls.Load())
	}
	waitFor(t, "cpu parked", func() bool { return clock.Sleepers() == 2 })
	clock.Advance(20 * time.Second)
	waitFor(t, "cpu 3rd, disk 2nd", func() bool { return cpu.calls.Load() == 3 && disk.calls.Load() == 2 })
	waitFor(t, "parked", func() bool { return clock.Sleepers() == 2 })
	b = buf.Snapshot()
	if len(b.Metrics) != 3 || !b.Metrics[2].At.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("stamps: %+v", b.Metrics)
	}
}

// FR-006: the stamp is the clock when the provider returned, not when it was called.
func TestScheduler_StampAtReturn(t *testing.T) {
	clock := collect.NewFakeClock(t0)
	m := moduletest.WithProvider(moduletest.CPU(), time.Minute, func(context.Context) ([]module.Observation, error) {
		clock.Advance(3 * time.Second)
		return []module.Observation{{Key: "model", Value: "x"}}, nil
	})
	sources, _ := collect.Sources(desiredFor(t, m), []module.Module{m}, nil)
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
	badMod := moduletest.WithProvider(moduletest.CPU(), 10*time.Second, bad.fn)
	goodMod := moduletest.WithProvider(moduletest.Disk(), 10*time.Second, good.fn)
	sources, _ := collect.Sources(desiredFor(t, badMod, goodMod), []module.Module{badMod, goodMod}, nil)
	clock := collect.NewFakeClock(t0)
	var logw bytes.Buffer
	s, buf, _ := start(t, sources, clock, &logw)
	<-s.FirstRound()
	waitFor(t, "parked", func() bool { return clock.Sleepers() == 2 })
	clock.Advance(10 * time.Second)
	waitFor(t, "second round", func() bool { return bad.calls.Load() == 2 && good.calls.Load() == 2 })
	waitFor(t, "parked", func() bool { return clock.Sleepers() == 2 })

	b := buf.Snapshot()
	if len(b.Dims) != 1 || b.Dims[0].Slug != "disk_count" || len(b.Metrics) != 0 {
		t.Fatalf("buffer: %+v", b)
	}
	logs := logw.String()
	for _, want := range []string{`collection failed`, `module=cpu`, `error=boom`, `provider panicked: kaboom`,
		`key not declared in manifest`, `key=nope`, `invalid value`, `key=mount`, `module=disk`} {
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
	m := moduletest.WithProvider(moduletest.CPU(), 10*time.Second, slow.fn)
	sources, _ := collect.Sources(desiredFor(t, m), []module.Module{m}, nil)
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
	am := moduletest.WithProvider(moduletest.CPU(), time.Second, a.fn)
	bm := moduletest.WithProvider(moduletest.Disk(), time.Second, b.fn)
	sources, _ := collect.Sources(desiredFor(t, am, bm), []module.Module{am, bm}, nil)
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
	ok := moduletest.WithProvider(moduletest.Disk(), time.Minute, func(context.Context) ([]module.Observation, error) {
		return []module.Observation{{Key: "count", Value: 3}}, nil
	})
	bad := moduletest.WithProvider(moduletest.CPU(), time.Minute, func(context.Context) ([]module.Observation, error) {
		return nil, errors.New("no cpu today")
	})
	sources, _ := collect.Sources(desiredFor(t, ok, bad), []module.Module{ok, bad}, nil)
	buf := collect.NewBuffer(0)
	failed := collect.Once(context.Background(), sources, buf, collect.NewFakeClock(t0), nil)
	if strings.Join(failed, ",") != "cpu" {
		t.Fatalf("failed: %v", failed)
	}
	if b := buf.Snapshot(); len(b.Dims) != 1 || b.Dims[0].Value != 3.0 || !b.Dims[0].At.Equal(t0) {
		t.Fatalf("buffer: %+v", b)
	}
}

// FR-001/003: Sources skips modules without a provider, applies overrides,
// rejects an interval for a module that produces nothing.
func TestSources(t *testing.T) {
	cpu := moduletest.WithProvider(moduletest.CPU(), 10*time.Second, nil)
	ident := moduletest.Ident()
	mods := []module.Module{ident, cpu}
	sources, err := collect.Sources(desiredFor(t, mods...), mods, map[string]time.Duration{"cpu": 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Module != "cpu" || sources[0].Interval != 5*time.Second || len(sources[0].Attrs) != 3 || sources[0].Attrs["usage"].Slug != "cpu_usage_pct" {
		t.Fatalf("sources: %+v", sources)
	}
	sources, err = collect.Sources(desiredFor(t, mods...), mods, nil)
	if err != nil || sources[0].Interval != 10*time.Second {
		t.Fatalf("default interval: %+v %v", sources, err)
	}
	_, err = collect.Sources(desiredFor(t, mods...), mods, map[string]time.Duration{"ident": time.Minute})
	if err == nil || !strings.Contains(err.Error(), "modules.ident.interval: module ident produces no values") {
		t.Fatalf("err: %v", err)
	}
}

// Overrides flow into sources: a remapped slug is what the sample carries.
func TestSources_Overrides(t *testing.T) {
	cpu := moduletest.WithProvider(moduletest.CPU(), 10*time.Second, func(context.Context) ([]module.Observation, error) {
		return []module.Observation{{Key: "usage", Value: 1}}, nil
	})
	d, err := manifest.Resolve(module.Manifests([]module.Module{cpu}), manifest.Overrides{Modules: map[string]manifest.ModuleOverride{
		"cpu": {Attributes: map[string]manifest.AttributeOverride{"usage": {Slug: "cpu_load"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	sources, _ := collect.Sources(d, []module.Module{cpu}, nil)
	buf := collect.NewBuffer(0)
	collect.Once(context.Background(), sources, buf, collect.NewFakeClock(t0), nil)
	if b := buf.Snapshot(); len(b.Metrics) != 1 || b.Metrics[0].Slug != "cpu_load" || b.Metrics[0].Key != "usage" {
		t.Fatalf("buffer: %+v", b)
	}
}
