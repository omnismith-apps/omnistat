package collect

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/omnismith-apps/omnistat/internal/identity"
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
	// It holds only the attributes collectable on this platform (spec 004
	// FR-019, ADR-0007).
	Attrs map[string]manifest.DesiredAttribute
	// Unsupported holds the keys the module declares but this platform
	// cannot report. An observation for one of them is dropped quietly: it
	// was already reported once at startup, so it is not an omission
	// (spec 004 FR-016).
	Unsupported map[string]bool
	// Links holds the keys of the module's host links: the core sets them,
	// so an observation for one is dropped (spec 011 FR-006).
	Links map[string]bool
	// Entities maps the module's entity templates, by manifest slug, to their
	// resolved slugs (spec 011 FR-006).
	Entities map[string]string
}

// Skipped is one thing the platform cannot collect, reported once at startup
// rather than on every tick (spec 004 FR-023, ADR-0007). An empty Key means
// the whole module is skipped because nothing it declares is collectable here.
type Skipped struct {
	Module    string
	Key       string
	Slug      string
	Platforms []string // where it *is* collectable
}

// Sources pairs every module that has a provider with its interval (the
// operator's override or the provider's default, FR-003) and its desired
// attributes, dropping what goos cannot collect (spec 004 FR-018…FR-022).
// Modules without a provider contribute nothing (FR-001); an interval
// configured for one of them is an error. An interval configured for a module
// this platform cannot collect is not: the platform decides, not the config
// (spec 004 FR-022).
func Sources(desired manifest.Desired, mods []module.Module, intervals map[string]time.Duration, goos string) ([]Source, []Skipped, error) {
	var out []Source
	var skipped []Skipped
	var problems []string
	for _, m := range mods {
		p, ok := module.ProviderOf(m)
		if !ok {
			if _, set := intervals[m.Name()]; set {
				problems = append(problems, fmt.Sprintf("modules.%s.interval: module %s produces no values", m.Name(), m.Name()))
			}
			continue
		}
		src := Source{
			Module:      m.Name(),
			Provider:    p,
			Interval:    p.DefaultInterval(),
			Attrs:       map[string]manifest.DesiredAttribute{},
			Unsupported: map[string]bool{},
			Links:       map[string]bool{},
			Entities:    map[string]string{},
		}
		for _, t := range desired.Templates {
			if t.Entity && t.Module == m.Name() {
				src.Entities[t.Key] = t.Slug
			}
		}
		if d, ok := intervals[m.Name()]; ok {
			src.Interval = d
		}
		if src.Interval <= 0 {
			return nil, nil, fmt.Errorf("module %s declares a non-positive default interval (bug)", m.Name())
		}
		var gated []Skipped
		for _, a := range desired.Attributes {
			if a.Module != m.Name() {
				continue
			}
			if a.Kind == manifest.KindReference {
				// Not the provider's to report, and not what makes a module
				// collectable (spec 011 FR-006).
				src.Links[a.Key] = true
				continue
			}
			if manifest.Collectable(a.Platforms, goos) {
				src.Attrs[a.Key] = a
				continue
			}
			src.Unsupported[a.Key] = true
			gated = append(gated, Skipped{Module: m.Name(), Key: a.Key, Slug: a.Slug, Platforms: a.Platforms})
		}
		if len(src.Attrs) == 0 {
			// Nothing left to collect here: report the module once rather
			// than attribute by attribute, and do not schedule it (FR-019).
			skipped = append(skipped, Skipped{Module: m.Name(), Platforms: platformUnion(gated)})
			continue
		}
		skipped = append(skipped, gated...)
		out = append(out, src)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, nil, fmt.Errorf("config: %s", strings.Join(problems, "; "))
	}
	return out, skipped, nil
}

// platformUnion collects, in first-seen order, every platform the skipped
// attributes name — what the module as a whole would need to be useful.
func platformUnion(gated []Skipped) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range gated {
		for _, p := range g.Platforms {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// Scheduler runs every source on its own cadence and feeds the buffer
// (FR-004, FR-006, FR-010, FR-011, FR-014).
type Scheduler struct {
	sources []Source
	buf     Sink
	clock   Clock
	log     *slog.Logger

	firstOnce sync.Once
	first     chan struct{}
	done      chan struct{}
	failures  failureLog
}

// NewScheduler builds a scheduler; Run starts it.
func NewScheduler(sources []Source, buf Sink, clock Clock, log *slog.Logger) *Scheduler {
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
				if err := collectOne(ctx, s.clock, s.buf, s.log, src); err != nil {
					s.failures.failed(s.log, src.Module, err) // the tick is skipped (FR-010)
				} else {
					s.failures.succeeded(s.log, src.Module)
				}
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
func collectOne(ctx context.Context, clock Clock, buf Sink, log *slog.Logger, src Source) error {
	cctx, cancel := withDeadline(ctx, clock, src.Interval)
	obs, err := safeCollect(cctx, src.Provider)
	cancel()
	at := clock.Now().UTC()
	if err != nil {
		return err // logged by the caller (spec 012 FR-018)
	}
	kept := 0
	var badKeys []string
	for _, o := range obs {
		if src.Links[o.Key] {
			log.Error("observation dropped: the host link is set by the core", "module", src.Module, "key", o.Key)
			continue
		}
		attr, ok := src.Attrs[o.Key]
		if !ok {
			if src.Unsupported[o.Key] {
				// Already reported once at startup (spec 004 FR-016/FR-023):
				// a provider that returns it anyway is not misbehaving enough
				// to warrant an error on every tick.
				log.Debug("observation dropped: not collectable on this platform", "module", src.Module, "key", o.Key)
				continue
			}
			log.Error("observation dropped: key not declared in manifest", "module", src.Module, "key", o.Key)
			continue
		}
		target, err := targetOf(src, attr, o.Entity)
		if err != nil {
			log.Error("observation dropped: wrong entity", "module", src.Module, "key", o.Key, "error", err)
			continue
		}
		if !target.IsHost() {
			if kerr := identity.ValidKey(target.Key); kerr != nil {
				badKeys = append(badKeys, o.Key+": "+kerr.Error())
				continue
			}
		}
		v, err := Validate(attr, o.Value)
		if err != nil {
			log.Error("observation dropped: invalid value", "module", src.Module, "key", o.Key, "error", err)
			continue
		}
		buf.Add(Sample{Module: src.Module, Key: o.Key, Slug: attr.Slug, Kind: attr.Kind, Value: v, At: at, Target: target})
		kept++
	}
	if len(badKeys) > 0 {
		// One record per collection, never the key itself (spec 011 FR-008).
		log.Error("observations dropped: invalid entity key", "module", src.Module, "count", len(badKeys), "reasons", strings.Join(badKeys, "; "))
	}
	log.Debug("collected", "module", src.Module, "observations", kept, "at", at)
	return nil
}

// TakeFailureCounts returns, per module, the failures logged at debug only
// since the last call (repeats of a reason already logged), and resets them.
// The daemon reports them in its publish summary (spec 012 FR-018).
func (s *Scheduler) TakeFailureCounts() map[string]int { return s.failures.take() }

// failureLog keeps a module's repeated collection failures out of the warn
// log (spec 012 FR-018, amending 003 FR-010): the first failure with a reason
// is a warning, repeats are counted at debug, and the first success after
// failures says how many there were.
type failureLog struct {
	mu      sync.Mutex
	reason  map[string]string // module → reason of the current streak
	streak  map[string]int    // module → failures in the current streak
	pending map[string]int    // module → repeats not yet reported
}

func (f *failureLog) failed(log *slog.Logger, module string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reason == nil {
		f.reason, f.streak, f.pending = map[string]string{}, map[string]int{}, map[string]int{}
	}
	f.streak[module]++
	if reason := err.Error(); f.reason[module] != reason {
		f.reason[module] = reason
		log.Warn("collection failed", "module", module, "error", err)
		return
	}
	f.pending[module]++
	log.Debug("collection failed again", "module", module, "error", err, "failures", f.streak[module])
}

func (f *failureLog) succeeded(log *slog.Logger, module string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := f.streak[module]; n > 0 {
		log.Info("collection recovered", "module", module, "after_failures", n)
	}
	delete(f.streak, module)
	delete(f.reason, module)
}

func (f *failureLog) take() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.pending
	f.pending = map[string]int{}
	return out
}

// targetOf checks that an observation names the entity its attribute belongs
// to and returns that entity (spec 011 FR-006).
func targetOf(src Source, attr manifest.DesiredAttribute, e module.Entity) (Target, error) {
	switch {
	case e.IsHost() && attr.EntityTemplate == "":
		return Target{}, nil
	case e.IsHost():
		return Target{}, fmt.Errorf("attribute belongs to entity template %q but the observation names no entity", attr.EntityTemplate)
	case e.Template != attr.EntityTemplate:
		return Target{}, fmt.Errorf("observation names template %q but the attribute belongs to %q", e.Template, describeTemplate(attr.EntityTemplate))
	}
	return Target{Module: src.Module, Template: src.Entities[e.Template], Key: e.Key}, nil
}

func describeTemplate(t string) string {
	if t == "" {
		return "the host"
	}
	return t
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
func Once(ctx context.Context, sources []Source, buf Sink, clock Clock, log *slog.Logger) []string {
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
				log.Warn("collection failed", "module", src.Module, "error", err)
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
