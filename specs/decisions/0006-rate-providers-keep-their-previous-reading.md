---
status: accepted         # proposed | accepted | deprecated | superseded
date: 2026-09-22
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0006: Rate providers keep their previous reading and prime themselves

## Context

ADR-0005 made providers on-demand collect functions: the core owns the clock, the
buffer and the API, and a provider is called with a deadline and returns typed
observations. It left open what a provider does when its value is not a reading but a
*rate* — a difference between two readings of a monotonic counter divided by the time
between them. Spec 004's `cpu_usage_pct` is the first such value, and disk I/O, network
throughput and interrupt rates will all be the same shape. This is a property of the
value, not of the platform: every operating system exposes CPU time as a cumulative
counter, so the decision below holds wherever omnistat runs.

This applies only to values the OS exposes as cumulative counters. Where the kernel
already maintains the rate itself — the load averages of 004 FR-006 are the example —
the provider reads and forwards, holding nothing; the state-keeping below is not a
house style to apply everywhere, it is the cost of a counter with no kernel-side average.

Two constraints collide. The counters the kernel exposes are cumulative, so a single
reading yields no usage at all. And `omnistat run` without `--daemon` is a supported,
documented mode (003 FR-018) that collects each module exactly once: a design where the
first collection produces nothing means the one-shot mode — used from provisioning
scripts and cron — never publishes a CPU number, only dimensions.

## Decision

A provider MAY retain state between calls, in memory only, for the sole purpose of
computing a rate from consecutive readings. When it has no previous reading it primes
itself inside the call: it takes a second reading after a short, context-bounded pause
(250ms for `cpu`) and reports the rate over that window. If the call's deadline would
elapse before the pause completes, the provider omits the rate and returns the
observations it could produce; it never returns a value it did not measure.

The core is unchanged: it still owns the schedule, the stamp, the buffer and the
network, and it still calls `Collect` exactly as before. Retained state is
process-local and never persisted.

## Alternatives considered

- **Nothing on the first tick** — the provider stores the first reading and reports the
  rate only from the second collection onward. Simplest, no sleeping, but it silently
  breaks the one-shot mode: `omnistat run` would publish dimensions and no usage, and a
  cron-based deployment would never produce a single CPU sample. Rejected because the
  one-shot mode is a first-class promise of 003, not a debugging convenience.
- **The core primes every provider at startup** — one discarded collection of every
  module before the loop starts. Generic and keeps providers stateless-looking, but it
  puts a warm-up phase into the run loop that every module pays for, delays the first
  publish by an extra round, and still needs the provider to retain the reading between
  the priming call and the real one — so it adds core machinery without removing the
  state it was meant to remove. Rejected on the simplicity gate (constitution VI).
- **Publish the cumulative counters and let the platform derive the rate** — no state at
  all in omnistat. Rejected: the platform offers no documented derivative over a metric
  series, the raw jiffy counters are meaningless in a dashboard, and a counter reset
  would show as a cliff nobody can interpret.
- **Sample continuously in a background goroutine** — contradicts ADR-0005 outright
  (providers own no goroutine that outlives a call). This is also why the reading source
  of ADR-0008 is used only for stateless reads: its own `cpu.Percent` keeps a
  package-level baseline seeded in an `init()` and shared by every caller in the
  process, which is this decision's state with none of its ownership.

## Consequences

- Positive: one-shot and daemon modes both publish real rates; each rate module owns its
  own counter arithmetic without the core growing a concept of "rate"; the pattern is
  written down once for network, disk and every later counter module.
- Positive: on the steady-state path (every collection after the first) there is no pause
  at all — the rate spans the whole collection interval, which is what the operator
  configured.
- Negative / accepted trade-offs: providers are no longer pure functions, so their tests
  must exercise call sequences rather than single calls; a `Collect` is no longer safe to
  call concurrently with itself (003 FR-011 already forbids overlapping collections of
  the same module, so this costs nothing); the first collection of a rate module is
  measured over 250ms rather than over the module's interval, so it is noisier than
  later samples — accepted, and visible only once per process.
- Negative: a provider that is cancelled mid-pause must leave its retained reading
  consistent (004 FR-014); that is a correctness obligation on every rate module.
- Follow-ups: if a future source has an inherent observation time rather than a derived
  rate, extend the observation with an optional timestamp (ADR-0005's own follow-up)
  rather than widening this decision.
