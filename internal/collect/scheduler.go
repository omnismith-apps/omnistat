package collect

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// Source is one module's provider bound to its collection interval and the
// (overridden) attributes its keys map to.
type Source struct {
	Module   string
	Provider module.Provider
	Interval time.Duration
	// Attrs maps manifest key → desired attribute (slug after overrides).
	Attrs map[string]manifest.DesiredAttribute
}

// Sources pairs every module that has a provider with its interval (the
// operator's override or the provider's default, FR-003) and its desired
// attributes. Modules without a provider contribute nothing (FR-001); an
// interval configured for one of them is an error.
func Sources(desired manifest.Desired, mods []module.Module, intervals map[string]time.Duration) ([]Source, error) {
	var out []Source
	var problems []string
	for _, m := range mods {
		p, ok := module.ProviderOf(m)
		if !ok {
			if _, set := intervals[m.Name()]; set {
				problems = append(problems, fmt.Sprintf("modules.%s.interval: module %s produces no values", m.Name(), m.Name()))
			}
			continue
		}
		src := Source{Module: m.Name(), Provider: p, Interval: p.DefaultInterval(), Attrs: map[string]manifest.DesiredAttribute{}}
		if d, ok := intervals[m.Name()]; ok {
			src.Interval = d
		}
		if src.Interval <= 0 {
			return nil, fmt.Errorf("module %s declares a non-positive default interval (bug)", m.Name())
		}
		for _, a := range desired.Attributes {
			if a.Module == m.Name() {
				src.Attrs[a.Key] = a
			}
		}
		out = append(out, src)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("config: %s", strings.Join(problems, "; "))
	}
	return out, nil
}

// Scheduler runs every source on its own cadence and feeds the buffer
// (FR-004, FR-006, FR-010, FR-011, FR-014).
type Scheduler struct {
	sources []Source
	buf     *Buffer
	clock   Clock
	log     *slog.Logger

	firstOnce sync.Once
	first     chan struct{}
	done      chan struct{}
}

// NewScheduler builds a scheduler; Run starts it.
func NewScheduler(sources []Source, buf *Buffer, clock Clock, log *slog.Logger) *Scheduler {
	if clock == nil {
		clock = RealClock{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{sources: sources, buf: buf, clock: clock, log: log, first: make(chan struct{}), done: make(chan struct{})}
}

// FirstRound is closed once every source has finished (or failed) its
// first collection (FR-014).
func (s *Scheduler) FirstRound() <-chan struct{} { return s.first }

// Done is closed when Run has returned and every goroutine has stopped.
func (s *Scheduler) Done() <-chan struct{} { return s.done }

// Run collects until ctx is done, then waits for in-flight collections to
// finish. One goroutine per source: a module's collections never overlap,
// different modules run concurrently (FR-011).
func (s *Scheduler) Run(ctx context.Context) {
	defer close(s.done)
	var wg sync.WaitGroup
	var pending sync.WaitGroup
	pending.Add(len(s.sources))
	for _, src := range s.sources {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			first := true
			for {
				start := s.clock.Now()
				_ = collectOne(ctx, s.clock, s.buf, s.log, src) // errors are logged; the tick is skipped (FR-010)
				if first {
					pending.Done()
					first = false
				}
				wait := src.Interval - s.clock.Now().Sub(start)
				if wait <= 0 {
					// The collection used its whole interval (FR-004): go
					// again right away, but still yield to cancellation.
					if ctx.Err() != nil {
						return
					}
					continue
				}
				if s.clock.Sleep(ctx, wait) != nil {
					return
				}
			}
		}(src)
	}
	go func() {
		pending.Wait()
		s.firstOnce.Do(func() { close(s.first) })
	}()
	wg.Wait()
	s.firstOnce.Do(func() { close(s.first) })
}

// collectOne is the shared per-call path of Scheduler and Once.
func collectOne(ctx context.Context, clock Clock, buf *Buffer, log *slog.Logger, src Source) error {
	cctx, cancel := withDeadline(ctx, clock, src.Interval)
	obs, err := safeCollect(cctx, src.Provider)
	cancel()
	at := clock.Now().UTC()
	if err != nil {
		log.Warn("collection failed", "module", src.Module, "error", err)
		return err
	}
	kept := 0
	for _, o := range obs {
		attr, ok := src.Attrs[o.Key]
		if !ok {
			log.Error("observation dropped: key not declared in manifest", "module", src.Module, "key", o.Key)
			continue
		}
		v, err := Validate(attr, o.Value)
		if err != nil {
			log.Error("observation dropped: invalid value", "module", src.Module, "key", o.Key, "error", err)
			continue
		}
		buf.Add(Sample{Module: src.Module, Key: o.Key, Slug: attr.Slug, Kind: attr.Kind, Value: v, At: at})
		kept++
	}
	log.Debug("collected", "module", src.Module, "observations", kept, "at", at)
	return nil
}

// safeCollect calls the provider and turns a panic into an error so that a
// buggy module cannot take the loop down.
func safeCollect(ctx context.Context, p module.Provider) (obs []module.Observation, err error) {
	defer func() {
		if r := recover(); r != nil {
			obs, err = nil, fmt.Errorf("provider panicked: %v", r)
		}
	}()
	return p.Collect(ctx)
}

// withDeadline cancels the returned context after d on the given clock
// (FR-004), so that a fake clock can expire it in tests.
func withDeadline(ctx context.Context, clock Clock, d time.Duration) (context.Context, context.CancelFunc) {
	return clock.WithDeadline(ctx, d)
}

// Once collects every source exactly once, concurrently, and returns the
// names of the modules whose collection failed, sorted (FR-018 one-shot).
func Once(ctx context.Context, sources []Source, buf *Buffer, clock Clock, log *slog.Logger) []string {
	if clock == nil {
		clock = RealClock{}
	}
	if log == nil {
		log = slog.Default()
	}
	var mu sync.Mutex
	var failed []string
	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			if err := collectOne(ctx, clock, buf, log, src); err != nil {
				mu.Lock()
				failed = append(failed, src.Module)
				mu.Unlock()
			}
		}(src)
	}
	wg.Wait()
	sort.Strings(failed)
	return failed
}
