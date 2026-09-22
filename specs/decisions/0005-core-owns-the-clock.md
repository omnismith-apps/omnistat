---
status: accepted
date: 2026-09-22
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0005: The core owns the clock; providers are on-demand collect functions

## Context
Spec 003 introduces value providers. Each module has its own natural cadence (a
hostname changes rarely, CPU usage is worth sampling every few seconds) while the
platform should receive one batched write per host per publish interval. Two designs
were on the table: modules run their own timers and push values to the core, or
modules expose a "collect now" function and declare a default interval while the
core schedules, stamps, buffers and publishes. The constitution's simplicity gate
(VI), the one-shot mode (collect once, publish once, exit) and the testing rule
(everything deterministic in `go test`, V) constrain the choice.

## Decision
Providers are pure, on-demand functions: given a context with a deadline they return
a collection of typed observations for the keys their manifest declares, and they
declare a default collection interval. The core alone runs tickers, enforces
deadlines and non-overlap, stamps every observation with its collection time,
buffers (all metric observations, latest dimension only, bounded), and publishes on
the operator's publish interval. Providers never see a timer, a goroutine that
outlives a call, a buffer, a timestamp or the API.

## Alternatives considered
- **Modules own timers and push into a channel** — every module re-implements
  scheduling, cancellation and one-shot handling; a misbehaving module can flood the
  core; tests need real time or a per-module fake clock. The "each module decides
  its cadence" goal is met just as well by declaring a default interval.
- **Providers stamp their own observations** — needed only for sources with an
  inherent observation time (log readers, counters with device timestamps). None
  exists yet; adding an optional timestamp to the observation later is additive.
- **No buffer: publish on every collection** — request rate would scale with the
  number of modules and their cadences instead of with the publish interval;
  contradicts the platform owner's batching requirement (003 NFR-003).

## Consequences
- Positive: one-shot mode is the degenerate case of the same loop; providers are
  trivially unit-tested; scheduling and buffering are tested once, under an injected
  clock; the request rate is a function of one operator setting.
- Negative / accepted trade-offs: an unpublished buffer is lost on a crash (bounded
  by the publish interval; a stop signal flushes it); a module with a true
  event-driven source would have to poll — acceptable until such a module exists.
- Follow-ups: when a module needs provider-supplied timestamps or platform gating,
  extend the observation/manifest additively rather than moving scheduling into
  modules.
