package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/publish"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

// run implements `omnistat run` (spec 003 FR-018…022): reconcile → resolve
// identity → collect → publish, once by default or on a schedule with
// --daemon. --dry-run performs every step except the writes.
func (a *App) run(e env, args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	daemon := fs.Bool("daemon", false, "keep running: collect each module on its interval, publish every publish.interval")
	dryRun := fs.Bool("dry-run", false, "print what would be published; write nothing")
	asJSON := fs.Bool("json", false, "with --dry-run: print each publish as JSON (version 1)")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	if *asJSON && !*dryRun {
		return fail(e, errors.New("run: --json requires --dry-run"))
	}
	clock := a.Clock
	if clock == nil {
		clock = collect.RealClock{}
	}

	s, err := e.loadConfig()
	if err != nil {
		return fail(e, err)
	}
	p, err := a.prepareLocal(e, s)
	if err != nil {
		return fail(e, err)
	}
	// Everything that can fail on configuration alone fails before the network (FR-003).
	sources, skipped, err := collect.Sources(p.desired, p.modules, p.settings.Intervals, a.goos())
	if err != nil {
		return fail(e, err)
	}
	if err := p.readSchema(e); err != nil {
		return fail(e, err)
	}
	resolved, err := a.reconcile(e, p, *dryRun)
	if err != nil {
		return fail(e, err)
	}
	host, err := a.resolveHost(e, p, resolved, *dryRun)
	if err != nil {
		return fail(e, err)
	}
	for _, src := range sources {
		e.log.Info("module scheduled", "module", src.Module, "interval", src.Interval)
	}
	// What this platform cannot collect is said once, here — never once per
	// tick (spec 004 FR-016, FR-023).
	for _, sk := range skipped {
		where := strings.Join(sk.Platforms, ",")
		if sk.Key == "" {
			e.log.Info("module skipped: not collectable on this platform", "module", sk.Module, "platform", a.goos(), "collectable_on", where)
			continue
		}
		e.log.Info("attribute skipped: not collectable on this platform", "module", sk.Module, "key", sk.Key, "slug", sk.Slug, "platform", a.goos(), "collectable_on", where)
	}
	e.log.Info("publish scheduled", "interval", p.settings.PublishInterval, "daemon", *daemon, "dry_run", *dryRun)

	pub := &publish.Publisher{API: p.api, EntityID: host.EntityID, ListItems: resolved.ListItems, Log: e.log}
	if *dryRun {
		pub.Printer = &publish.Printer{W: e.stdout, JSON: *asJSON, Skipped: skippedLines(skipped)}
		pub.PendingOptions = schema.Diff(p.desired, p.current).PendingOptions()
		printSkipped(e, skipped, a.goos(), *asJSON)
	}
	buf := collect.NewBuffer(a.MaxPerMetric)
	l := &loop{env: e, clock: clock, buf: buf, pub: pub, sources: sources, interval: p.settings.PublishInterval, timeout: p.settings.HTTP.Timeout,
		daemonMode: *daemon, summaryEvery: p.settings.Log.SummaryInterval}
	if *daemon {
		return l.daemon()
	}
	return l.once(*dryRun)
}

// reconcile brings the schema to the desired state in the configured mode and
// returns the resolved ids (FR-018, FR-022). In dry-run the plan is shown, not
// applied; ids of objects that do not exist yet are simply absent.
func (a *App) reconcile(e env, p prepared, dryRun bool) (schema.Resolved, error) {
	plan := schema.Diff(p.desired, p.current)
	if len(plan.Conflicts) > 0 {
		return schema.Resolved{}, &schema.ConflictError{Conflicts: plan.Conflicts}
	}
	switch {
	case plan.Empty():
		return schema.ResolvedFrom(p.current), nil
	case p.settings.Mode != config.ModeApply:
		fmt.Fprint(e.stdout, plan.Text())
		return schema.Resolved{}, fmt.Errorf("schema is incomplete (schema.mode: %s); run `omnistat schema apply` with a token that can write the schema", p.settings.Mode)
	case dryRun:
		fmt.Fprint(e.stdout, plan.Text())
		fmt.Fprintln(e.stdout, "(dry-run: schema not applied)")
		return schema.ResolvedFrom(p.current), nil
	}
	res, err := schema.Apply(e.ctx, p.api, p.desired, p.current, e.log)
	if err != nil {
		return schema.Resolved{}, explain(err)
	}
	fmt.Fprintf(e.stdout, "schema reconciled: %d actions\n", len(res.Done))
	return res.Resolved, nil
}

// resolveHost discovers the identity and finds or creates the host entity
// (FR-018; spec 002 FR-016). In dry-run on a fresh project the entity does
// not exist yet and nothing is created.
func (a *App) resolveHost(e env, p prepared, resolved schema.Resolved, dryRun bool) (identity.Host, error) {
	mod, ok := a.Registry.Get(machineid.Name)
	provider, isProvider := mod.(*machineid.Module)
	if !ok || !isProvider {
		return identity.Host{}, errors.New("machine-id module is not available in this build")
	}
	id, err := provider.Discover(e.ctx, p.settings.Identity)
	if err != nil {
		return identity.Host{}, err
	}
	e.log.Info("identity", "source", id.Source)
	target, err := identityTarget(p.desired, resolved)
	if err != nil {
		if dryRun {
			return identity.Host{Identity: id.Value, WouldCreate: true}, nil
		}
		return identity.Host{}, err
	}
	h, err := identity.Resolve(e.ctx, p.api, target, id.Value, dryRun, e.log)
	if err != nil {
		return identity.Host{}, explain(err)
	}
	return h, nil
}

// loop is the collect/publish machinery shared by one-shot and daemon.
type loop struct {
	env      env
	clock    collect.Clock
	buf      *collect.Buffer
	pub      *publish.Publisher
	sources  []collect.Source
	interval time.Duration
	timeout  time.Duration

	// Publish logging in daemon mode (FR-026a).
	daemonMode   bool
	summaryEvery time.Duration // 0: every publish at info
	stats        publishStats
	succeeded    bool // a publish has succeeded since start
	failStreak   int  // failed publishes since the last success
}

// publishStats accumulates daemon publishes between two summaries (FR-026a).
type publishStats struct {
	since                                       time.Time
	publishes, failed, dims, obs, reqs, dropped int
	maxDuration                                 time.Duration
}

func (st *publishStats) add(res publish.Result, d time.Duration, failed bool) {
	if failed {
		st.failed++
	} else {
		st.publishes++
	}
	st.dims += res.Dimensions
	st.obs += res.Observations
	st.reqs += res.Requests
	st.dropped += res.Dropped
	st.maxDuration = max(st.maxDuration, d)
}

// summaries reports whether the daemon logs publish summaries (FR-026a).
func (l *loop) summaries() bool { return l.daemonMode && l.summaryEvery > 0 }

// summarize logs the summary once the period is over, or at once when final
// (on stop, for the unfinished period), and starts a new period.
func (l *loop) summarize(final bool) {
	if !l.summaries() {
		return
	}
	now := l.clock.Now()
	period := now.Sub(l.stats.since)
	if !final && period < l.summaryEvery {
		return
	}
	st := l.stats
	l.env.log.Info("publish summary", "period", period, "publishes", st.publishes, "dimensions", st.dims, "observations", st.obs,
		"requests", st.reqs, "dropped", st.dropped, "failed", st.failed, "max_duration", st.maxDuration)
	l.stats = publishStats{since: now}
}

// once collects every module one time and publishes (FR-018).
func (l *loop) once(dryRun bool) int {
	failed := collect.Once(l.env.ctx, l.sources, l.buf, l.clock, l.env.log)
	res, err := l.publish(l.env.ctx)
	if err != nil {
		return fail(l.env, err)
	}
	if !dryRun {
		fmt.Fprintf(l.env.stdout, "published %d dimensions, %d observations to entity %s\n", res.Dimensions, res.Observations, l.pub.EntityID)
	}
	if len(failed) > 0 {
		fmt.Fprintf(l.env.stderr, "omnistat: %d module(s) failed to collect: %s\n", len(failed), strings.Join(failed, ", "))
		return ExitPartial
	}
	return ExitOK
}

// daemon runs until the context is cancelled: first publish right after the
// first round, then every interval (FR-014, FR-019, FR-020).
func (l *loop) daemon() int {
	ctx := l.env.ctx
	sctx, cancel := context.WithCancel(ctx)
	sched := collect.NewScheduler(l.sources, l.buf, l.clock, l.env.log)
	go sched.Run(sctx)
	stop := func() {
		cancel()
		<-sched.Done()
	}

	select {
	case <-sched.FirstRound():
	case <-ctx.Done():
	}
	start := l.clock.Now()
	l.stats = publishStats{since: start}
	for ctx.Err() == nil {
		if _, err := l.publish(ctx); err != nil {
			if errors.Is(err, publish.ErrEntityGone) {
				stop()
				return fail(l.env, err)
			}
			l.env.log.Error("publish failed; keeping the buffer", "error", err)
		}
		l.summarize(false)
		// Next tick on the interval grid; ticks missed during a slow publish are skipped (FR-014).
		elapsed := l.clock.Now().Sub(start)
		next := start.Add(l.interval * (elapsed/l.interval + 1))
		if l.clock.Sleep(ctx, next.Sub(l.clock.Now())) != nil {
			break
		}
	}

	// Stop signal: no more collections, one final publish under the HTTP deadline (FR-020, NFR-005).
	stop()
	fctx, fcancel := context.WithTimeout(context.Background(), l.timeout)
	defer fcancel()
	if _, err := l.publish(fctx); err != nil {
		l.env.log.Warn("final publish failed", "error", err)
	}
	l.summarize(true)
	l.env.log.Info("stopped")
	return ExitOK
}

// publish sends what is buffered, acks what was accepted and logs the
// outcome (FR-009, FR-026).
func (l *loop) publish(ctx context.Context) (publish.Result, error) {
	for slug, n := range l.buf.Drops() {
		l.env.log.Warn("metric buffer full; oldest observations dropped", "slug", slug, "dropped", n)
	}
	batch := l.buf.Snapshot()
	if batch.Empty() {
		l.env.log.Debug("nothing to publish")
		return publish.Result{}, nil
	}
	started := l.clock.Now()
	res, err := l.pub.Publish(ctx, batch)
	l.buf.Ack(batch, res.Ack)
	took := l.clock.Now().Sub(started)
	attrs := []any{"dimensions", res.Dimensions, "observations", res.Observations, "requests", res.Requests, "dropped", res.Dropped, "duration", took}
	l.stats.add(res, took, err != nil)
	if err != nil {
		l.env.log.Error("publish failed", append(attrs, "error", err)...)
		l.failStreak++
		return res, err
	}
	// FR-026a: with summaries on, only the first success and a recovery are
	// worth an info line; the summary carries the rest.
	switch {
	case !l.summaries(), !l.succeeded:
		l.env.log.Info("published", attrs...)
	case l.failStreak > 0:
		l.env.log.Info("publish recovered", append(attrs, "after_failures", l.failStreak)...)
	default:
		l.env.log.Debug("published", attrs...)
	}
	l.succeeded, l.failStreak = true, 0
	return res, nil
}

// skippedLines renders the platform skips for the dry-run JSON document.
func skippedLines(skipped []collect.Skipped) []publish.SkippedLine {
	if len(skipped) == 0 {
		return nil
	}
	out := make([]publish.SkippedLine, 0, len(skipped))
	for _, sk := range skipped {
		out = append(out, publish.SkippedLine{Module: sk.Module, Key: sk.Key, Slug: sk.Slug, Platforms: sk.Platforms})
	}
	return out
}

// printSkipped shows, once per run, what the platform cannot collect, so that
// a dry-run explains an absent value instead of leaving the operator to guess
// (spec 004 US-5/3). JSON carries the same list inside every document.
func printSkipped(e env, skipped []collect.Skipped, goos string, asJSON bool) {
	if len(skipped) == 0 || asJSON {
		return
	}
	fmt.Fprintf(e.stdout, "not collectable on %s:\n", goos)
	for _, sk := range skipped {
		where := strings.Join(sk.Platforms, ", ")
		if sk.Key == "" {
			fmt.Fprintf(e.stdout, "  module %s (collectable on %s)\n", sk.Module, where)
			continue
		}
		fmt.Fprintf(e.stdout, "  %s.%s → %s (collectable on %s)\n", sk.Module, sk.Key, sk.Slug, where)
	}
}
