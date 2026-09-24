# Starting prompt: a new value module

Copy everything below the line into a fresh agent session, fill in the four
bracketed fields at the top, and delete the rest of this paragraph. Everything
after those fields is already settled by the constitution and by ADRs 0001–0008;
it is repeated here so a cold session does not re-derive it or re-litigate it.

Written after feature 004 (`cpu`), which is the reference implementation: read
`internal/module/cpu/` and `specs/features/004-cpu-module/` when a rule below
seems abstract.

---

You are adding a new value module to **omnistat**. Read `AGENTS.md` first, in
full; it is the operating manual and it overrides anything here that conflicts.

## What I want

- **Module name**: `[e.g. memory]`
- **What it should report**: `[plain English — "how much RAM is in use, how much
  is free, and how much the machine has". Do not pre-decide attributes, slugs or
  kinds; that is what I want you to propose.]`
- **Why / who needs it**: `[e.g. "I want to alert when a host is near swap, and
  compare hosts of different sizes" — this decides what is worth publishing]`
- **Anything already decided**: `[e.g. "must work on Windows too", "skip
  per-process detail", or "none — you choose and ask me"]`

## How to proceed

Follow the repo's flow — **spec → plan → tasks → code → sync**. Do not write
feature code before I approve a spec.

**Ask me before writing the spec.** Feature 004 went well because the
contractual choices were settled up front rather than discovered in review. Ask
about, at minimum:

1. **Attribute set and kinds** — which values are metrics (time series) and
   which are dimensions (current state). Propose a lean set and a fuller one and
   recommend between them.
2. **Default slugs** — these are a contract: 001 FR-002 makes a rename a
   breaking change needing an ADR. Propose them explicitly, with your reasoning
   where a name is not obvious.
3. **A `list` attribute's options** — only propose `list` when you can enumerate
   every value the fleet will produce. Otherwise `text`. A value outside the
   options is dropped at publish time, which is a bad way to find out.
4. **Default collection interval** — what the value's volatility justifies,
   against the default 60s publish.
5. **Platform support, per attribute** — see "settled" below.

Then write `spec.md` (WHAT/WHY only — the review checklist forbids naming Go
packages or libraries), get my approval, then `plan.md`, then `tasks.md`, then
implement task by task.

## What is already settled — do not re-litigate

Read the ADR when you need the reasoning; the operative rule is here.

- **The core owns the clock (ADR-0005).** Your provider implements
  `Collect(ctx) ([]module.Observation, error)` and `DefaultInterval()`. It owns
  no timer, no goroutine outliving the call, no buffer, no network, no
  timestamp. The core schedules, stamps, buffers and publishes.
- **Rate values keep their previous reading (ADR-0006).** If a value is a
  difference between two counter readings (I/O, throughput, interrupts), hold
  the previous reading in the module and prime on the first call with a bounded,
  context-aware pause, so one-shot `omnistat run` publishes a real number. Where
  the OS already maintains the rate, read and forward it — hold nothing.
- **Platform support is per attribute (ADR-0007).** A manifest attribute may
  declare `Platforms`; empty means everywhere. An attribute the platform cannot
  report is not collected, is reported once at startup, and is **still declared
  in the schema everywhere** so a mixed fleet converges on one schema. Never
  publish a near-equivalent substitute under a slug that means something else —
  an absent series beats a dishonest one.
- **Host readings come from gopsutil (ADR-0008)**, behind a narrow `Reader`
  interface your module declares and owns, with the real implementation in one
  file. Two rules: **stateless reads only** (anything in gopsutil that keeps a
  package-level baseline or starts a background goroutine is forbidden — the
  state belongs in your provider where its lifetime is visible), and **only
  readings the OS actually maintains** (gopsutil emulates some values on
  platforms that lack them).
- **No new core packages are needed.** `modules.<name>.{enabled,interval,
  template,attributes.*}` already work generically — a new module adds no config
  keys. Register it in `cmd/omnistat/registry()` and it is scheduled, buffered,
  published and dry-runnable.
- **Do not create a shared `internal/hostread` package** on your own initiative.
  If this is the second or third module reading the host, say so and ask —
  extracting it is a decision, not a refactor (constitution VI).

## Rules that bite

- **Spike anything unverified about the platform, first, and throw the code
  away.** Feature 004's T003 spike cost one session and turned two silent traps
  into known facts before they could become confusing failures at acceptance
  time. Record findings in `docs/reference/omnismith-api-notes.md`.
- **Know your counter semantics before doing arithmetic on them.** Linux counts
  guest time inside `user` and guest-nice inside `nice`, so summing every field
  double-counts. Check the equivalent for whatever you are summing, and write a
  named test that pins the answer.
- **A provider cannot use `collect.Clock`** — `collect` imports `module`, so
  that is an import cycle. Inject `Now func() time.Time` and
  `Sleep func(ctx, d) error` as struct fields, as `cpu` and `machineid` do. No
  test may call `time.Sleep`.
- **Partial collection**: gather each value independently so one unreadable
  source costs only what depends on it; report the omissions in **one** log
  record per collection, not one per value; fail the collection only when
  nothing could be read. A value that comes from the build rather than the host
  (e.g. `runtime.GOARCH`) does not count as "something was read".
- **`moduletest` fixtures share a namespace with real modules.**
  `Registry.Register` panics on a duplicate name and manifest validation rejects
  duplicate slugs. Check `internal/module/moduletest/fixtures.go` before
  choosing a module name.
- **Test fixtures that return scripted readings advance per call.** When a code
  path takes one reading instead of two, the next call gets the next entry —
  get the indices right or you will "find" a bug that is in your fixture.
- **Metrics**: creating a metric attribute needs no special handling. Reading
  one back through `GetEntityChart` needs epoch **seconds** (milliseconds return
  `200` with an empty series) and an explicit `bucket_width` (the default is
  `1 hour`). See the API notes.

## Definition of done

- `make all`, `make test-race`, `make crosscheck`, `make specs-check` green.
- **Sandbox acceptance** against the real project (`make sandbox`, local API via
  `.env`): a test that applies the schema, runs, and reads the values back
  through the API. The fake API is not evidence that the platform accepts
  something.
- `spec.md` → `status: implemented` with an implementation-notes section
  recording **deviations, surprises and anything not verified** — including
  flakes you could not reproduce. Any ADR the feature added → `accepted`.
- `README.md` (module table + config example), `CHANGELOG.md` under
  *Unreleased*, `internal/README.md` (package row), and the API notes if you
  learned anything about the platform.
- Report honestly what is untested. macOS and Windows readings, for instance,
  are covered only by faked-`Reader` unit tests and the cross-compile gate — say
  so rather than implying acceptance coverage.

**Do not stage or commit anything.** I handle git myself.
