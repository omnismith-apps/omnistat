---
feature: 004-cpu-module
status: implemented       # draft | review | approved | implemented | superseded
approved: 2026-09-22
implemented: 2026-09-22
created: 2026-09-22
owners: [evgenii]
supersedes: null
adr: [0005, 0006, 0007, 0008]
depends_on: [001-module-schema-reconciliation, 002-host-identity, 003-run-loop-publisher]
---

# Feature: `cpu` module — the first metric provider

## Summary

Features 001–003 built the machinery — schema reconciliation, host identity, the
collect/buffer/publish loop — and proved it with `hostname`, a single text dimension.
Nothing omnistat ships yet produces a **metric**, so the time-series half of the
platform contract has only ever been exercised against the fake API. This feature adds
the `cpu` module: aggregate CPU usage and the three load averages as metrics, plus the
CPU's model, logical core count and architecture as dimensions. It is also the first
module whose value is a *rate* derived from two readings of a monotonic counter, and
the first whose attributes are not all collectable on every platform. It therefore
settles two contracts every later module inherits: how a provider that needs a previous
reading behaves (ADR-0006), and how a module declares, per attribute, where it can
collect (ADR-0007).

## Users & context

- **Operator** — runs omnistat as a service on Linux hosts and VMs and wants to see, in
  Omnismith, whether a machine is busy: a usage percentage on a cadence fine enough to
  show a spike, and the load averages they already reason about from `uptime`. Also runs
  `omnistat run` once from a shell or a provisioning script and expects that single
  invocation to publish a real usage number, not a placeholder.
- **Fleet operator** — hundreds of hosts of mixed sizes; needs the core count published
  alongside the load averages, because "load 8" means nothing without it. Wants one
  project schema across the fleet even though hosts differ in what they can report.
- **Platform owner** — watches request volume: a 10s sample cadence must not turn into a
  10s request cadence (003 NFR-003 already guarantees this; this feature is the first
  real test of it).
- Runs on Linux (primary) and macOS, typically unprivileged; everything read here comes
  from ordinary, unprivileged OS interfaces. The Windows readings are specified here so
  that the module is complete when omnistat itself gains Windows support — which needs
  host identity and service shutdown first and is **not** part of this feature.

## User stories

### US-1 — See how busy a host is (P1)
As an operator, I want the host entity to carry a CPU usage percentage sampled on a
regular cadence, so that I can chart it and alert on it in Omnismith.

**Acceptance scenarios**
1. **Given** a reconciled project, an existing host entity and the default module set,
   **When** the daemon runs for one publish interval, **Then** the host entity has
   received several CPU usage observations, each stamped with the instant it was
   collected, each a percentage between 0 and 100.
2. **Given** a host whose CPUs are fully idle between two samples, **When** usage is
   collected, **Then** the reported usage is 0, not absent and not an error.
3. **Given** a host with all cores saturated, **When** usage is collected, **Then** the
   reported usage is 100 (aggregate across all cores, not 100 × core count).

### US-2 — One-shot runs publish a real number (P1)
As an operator, I want `omnistat run` executed once from a script to publish an actual
usage measurement, so that provisioning and cron invocations are as useful as the daemon.

**Acceptance scenarios**
1. **Given** no previous reading (a fresh process), **When** the provider is collected
   once, **Then** it returns a usage observation measured over a short, bounded window
   inside that single call, and the one-shot run publishes it.
2. **Given** the collection deadline is shorter than that window, **When** the provider
   is collected, **Then** it returns no usage observation, reports the reason, and the
   dimensions it did gather are still published (003 FR-002).
3. **Given** the daemon has collected once already, **When** it collects again, **Then**
   usage is measured across the whole interval since the previous reading, with no pause.

### US-3 — Load averages next to the core count (P1)
As a fleet operator, I want the 1/5/15-minute load averages and the number of logical
CPUs on the host entity, so that I can compare load against capacity across machines.

**Acceptance scenarios**
1. **Given** the default module set on a host whose OS maintains load averages, **When**
   the module is collected, **Then** three load-average metrics are observed, and the
   logical core count is published as a dimension.
2. **Given** a host where the load-average source is unreadable but the usage source is
   fine, **When** the module is collected, **Then** usage is still observed and the
   missing load averages are reported without failing the whole collection.

### US-4 — Recognise the hardware (P2)
As an operator, I want the CPU model and architecture on the host entity, so that I can
tell machine classes apart without logging in.

**Acceptance scenarios**
1. **Given** any supported host, **When** the module is collected, **Then** the CPU model
   string reported by the OS and the architecture are published as dimensions.
2. **Given** a host whose CPU model cannot be determined, **When** the module is
   collected, **Then** the metrics and the remaining dimensions are still observed.

### US-5 — Silent about what this platform cannot measure (P2)
As a fleet operator, I want omnistat to collect what a platform can report and stay
quiet about the rest, rather than logging a failure every 10 seconds or inventing a
number, while my project schema stays the same across the whole fleet.

**Acceptance scenarios**
1. **Given** a host whose OS maintains no load average, **When** omnistat starts,
   **Then** it logs once that those attributes are unsupported on this platform, they
   are never collected, and no value is ever published for them.
2. **Given** the same host and an empty project, **When** the schema is applied, **Then**
   the `cpu` module's attributes are created in full, exactly as from any other host.
3. **Given** that host, **When** the dry-run prints what would be published, **Then** the
   unsupported attributes appear once as skipped with their reason, not as failures.

### US-6 — Turn it off (P2)
As an operator, I want to disable `cpu` on a host where I do not want CPU data, so that
neither its schema nor its values are produced there.

**Acceptance scenarios**
1. **Given** `cpu` disabled in config, **When** I dry-run the schema, **Then** none of
   its attributes appear in the plan, and attributes created by an earlier run are left
   untouched (001 US-4/1).
2. **Given** `cpu` disabled, **When** the daemon runs, **Then** it is never collected and
   the startup schedule log does not list it.

## Functional requirements

### Module manifest
- **FR-001** The `cpu` module MUST declare exactly these attributes on the host template,
  with these default slugs and kinds:

  | Key | Default slug | Kind | Human name | Meaning |
  |-----|--------------|------|------------|---------|
  | `usage` | `cpu_usage_pct` | metric | CPU usage | Percent of CPU time that was not idle, aggregated across all logical CPUs, over the interval since the previous reading |
  | `load1` | `load_avg_1` | metric | Load average (1m) | Run-queue load averaged over 1 minute, as the OS reports it |
  | `load5` | `load_avg_5` | metric | Load average (5m) | Run-queue load averaged over 5 minutes |
  | `load15` | `load_avg_15` | metric | Load average (15m) | Run-queue load averaged over 15 minutes |
  | `model` | `cpu_model` | text | CPU model | Model string reported by the OS |
  | `cores` | `cpu_cores` | number | CPU cores | Number of logical CPUs usable by the OS |
  | `arch` | `cpu_arch` | list | CPU architecture | Options, in order: `amd64`, `arm64` |

- **FR-002** The module MUST be enabled by default and MUST be disablable in config
  (001 FR-006). It is not required in the sense of 002 FR-002.
- **FR-003** Every attribute MUST be remappable — slug, template and creation-time
  name/description — like any other (001 FR-007, FR-008).
- **FR-004** The module's default collection interval MUST be **10s**, overridable per
  003 FR-003 within the 1s–24h bounds.

### Values
- **FR-005** `usage` MUST be the percentage of CPU time spent in any non-idle state,
  computed from the difference between two readings of the OS's cumulative per-state
  CPU time counters, divided by the total difference across all states, times 100. Idle
  time is the platform's idle counter plus, where the platform reports one, its I/O-wait
  counter; every other state counts as busy. The result MUST be clamped to `[0, 100]`
  and MUST be aggregate across all logical CPUs (a fully busy 8-core host reports 100,
  not 800).
- **FR-006** The load averages MUST be the values the operating system itself maintains,
  taken as-is. omnistat MUST NOT compute, smooth or synthesise them, and MUST NOT
  publish a substitute quantity under these slugs on a platform whose OS maintains no
  load average — there, the attributes are simply not collected (FR-018).
- **FR-007** `cores` MUST be the number of logical CPUs the OS makes available to
  processes. Physical cores, sockets and CPU affinity masks are out of scope.
- **FR-008** `arch` MUST be the architecture the running binary targets, expressed as
  one of the manifest's declared options. An architecture with no declared option MUST
  be dropped as an undeclared list option (003 FR-002); it MUST NOT fail the collection.
- **FR-009** `model` MUST be the model string the OS reports, trimmed and collapsed to a
  single line. When the OS reports several CPUs, the first is used. An empty or
  unavailable model MUST be omitted, not published as an empty string.
- **FR-010** The dimensions (`model`, `cores`, `arch`) MUST be observed on every
  collection, not only the first. They are effectively static; the core's buffer keeps
  only the latest (003 FR-007), so this costs nothing extra per publish.

### Rate collection (ADR-0006)
- **FR-011** The provider MUST retain the counter reading of its previous collection for
  the lifetime of the process, and MUST compute `usage` from that reading and the
  current one. The retained reading MUST be the provider's own; it MUST NOT depend on
  state shared with anything else in the process, and MUST NOT be persisted to disk.
- **FR-012** When no previous reading exists (first collection after start), the provider
  MUST take a second reading after a bounded **250ms** pause within the same call, and
  compute `usage` from that pair, so that a one-shot `run` publishes a real measurement
  (US-2/1). The pause MUST honour the call's context: if the deadline would elapse first,
  the provider MUST skip `usage` and return the remaining observations (US-2/2).
- **FR-013** A collection in which the counter total did not advance (two readings inside
  the same clock tick, or a counter that went backwards after a suspend/resume or a CPU
  hotplug event) MUST NOT produce a `usage` observation and MUST NOT produce a division
  error; the reading is stored as the new baseline and the next collection measures
  from it.
- **FR-014** A cancelled collection MUST NOT corrupt the retained reading: either the
  new reading becomes the baseline or the old one is kept, never a partial mixture.

### Partial collection
- **FR-015** The module MUST produce every value it can and report the ones it cannot.
  A source that is unreadable, malformed or missing MUST cost only the observations that
  depend on it (US-3/2, US-4/2). The collection as a whole MUST fail only when **no**
  observation could be produced, in which case it is an ordinary provider failure
  (003 FR-010).
- **FR-016** Each omitted observation MUST be reported once per collection with the
  attribute key and the reason. An attribute that is unsupported on this platform
  (FR-018) is not an omission and MUST NOT be reported this way; it is reported once at
  startup instead (FR-023).

### Platform support (ADR-0007)
- **FR-017** A manifest MUST be able to declare, **per attribute**, the platforms on
  which that attribute can be collected. An attribute that declares none is collectable
  everywhere; so is every attribute of every module shipped before this feature.
- **FR-018** The `cpu` module MUST declare: `usage`, `model`, `cores` and `arch`
  collectable on Linux, macOS and Windows; `load1`, `load5` and `load15` collectable on
  Linux and macOS only, because Windows maintains no load average (FR-006).
- **FR-019** On a platform where an attribute is not collectable, the core MUST NOT
  expect it and the provider MUST NOT return it. A module **all** of whose attributes
  are uncollectable on this platform MUST NOT be scheduled or called at all.
- **FR-020** Platform support MUST NOT affect the desired schema: every enabled module
  contributes its whole manifest everywhere, so that reconciliation from any host in a
  mixed fleet produces the same schema (constitution III; US-5/2).
- **FR-021** A module disabled in config contributes neither schema nor values, exactly
  as today (001 FR-006). "Uncollectable here" and "disabled" are distinct states and
  MUST be distinguishable in the startup log and in the dry-run output (US-5/3, US-6).
- **FR-022** An operator who explicitly enables a module whose attributes are
  uncollectable on this platform MUST get the same skip, not an error: the platform
  decides what can be measured, not the config.

### Observability
- **FR-023** The startup schedule log (003 FR-027) MUST list `cpu` with its effective
  collection interval and, once, any of its attributes that are uncollectable on this
  platform with the reason. A module skipped entirely under FR-019 MUST be listed as
  skipped rather than omitted.
- **FR-024** Collected values MUST be logged at debug level only (003 FR-026). No log
  line may contain raw readings from the OS sources.

## Non-functional requirements
- **NFR-001** (performance) One collection takes a fixed, small number of OS readings and
  allocates a bounded amount; it MUST complete in well under 100ms on a typical VM,
  excluding the FR-012 first-collection pause.
- **NFR-002** (resources) The provider retains exactly one previous reading, of a size
  independent of the number of CPUs (aggregate counters only). Collection at 10s for a
  day MUST NOT grow the process beyond the buffer bounds of 003 NFR-002.
- **NFR-003** (network) The module adds no request of its own: at the default 10s/60s
  cadence its observations ride the existing publish (003 NFR-003).
- **NFR-004** (safety) Collection MUST require no elevated privileges, MUST execute no
  external command, and MUST write nothing to the host.
- **NFR-005** (testability) Every value MUST derive from an injectable source of OS
  readings and an injected clock, so that all of FR-005…FR-016 is testable in `go test`
  with no real CPU load and no sleeping, and the per-platform decisions of FR-017…FR-023
  are testable without the platform. The static binary MUST still build for every target
  of constitution V with `CGO_ENABLED=0`.
- **NFR-006** (acceptance) The metric path MUST be proven end-to-end against the sandbox
  project, closing the gap 003 left open (003 "Out of scope"): observations ingested,
  accepted, and read back as a time series. Acceptance runs on Linux; macOS and Windows
  readings are covered by unit tests and by the cross-compilation gate of NFR-005.

## Data & integration contract

Read: nothing new from Omnismith beyond 001 (schema and resolved ids, including the list
item ids of `cpu_arch`) and 002 (the host entity). From the host: the OS's cumulative
per-state CPU time counters, its load averages where it maintains them, its CPU model
and its logical CPU count — all through unprivileged OS interfaces.

Write (additive only, all on the host entity resolved by 002):
- **Metrics** — `cpu_usage_pct`, and `load_avg_1`/`load_avg_5`/`load_avg_15` where the
  platform maintains them, ingested by the publisher of 003 FR-012 with their collection
  timestamps.
- **Dimensions** — `cpu_model` (text), `cpu_cores` (number), `cpu_arch` (list, published
  as the list item's identifier per 003 FR-012a).

Owned attributes: `cpu_usage_pct`, `load_avg_1`, `load_avg_5`, `load_avg_15`,
`cpu_model`, `cpu_cores`, `cpu_arch` — module `cpu`. No other module may declare them.

## Edge cases & failure modes

- **First collection, tight deadline** → no `usage`, other observations kept (FR-012).
- **Two collections within one clock tick** → no `usage`, new baseline kept (FR-013).
- **Counter goes backwards** (suspend/resume, CPU hotplug, container counter reset) →
  treated as FR-013: skip the sample, re-baseline, never report a negative or absurd
  percentage.
- **CPU hotplug changes the core count mid-run** → the next collection publishes the new
  `cpu_cores`; usage stays aggregate and comparable.
- **Container with a CPU quota** → the counters omnistat reads are the host's, so usage
  reflects the host, not the cgroup quota. Documented, not corrected; cgroup-aware usage
  is out of scope.
- **Platform with no load average** (Windows) → FR-006/FR-018: not collected, not
  synthesised, reported once at startup.
- **Load-average source unreadable on a platform that has one** → usage and dimensions
  still published (US-3/2).
- **Model source unreadable** → model omitted (FR-009).
- **Architecture not among the declared options** (a self-built riscv64 binary) → that
  one observation is dropped with an error log (FR-008, 003 FR-002).
- **`cpu_arch` remapped onto an existing list attribute lacking `amd64`/`arm64`** →
  reconciliation adds the missing options (001 FR-016); if the attribute is not a list,
  it is a conflict and nothing is written (001 FR-015).
- **API unreachable / 401 / 403 / 404 / 422 / 429** → unchanged from 003; nothing in this
  module touches the API.
- **Publish interval shorter than the collection interval** (e.g. publish 5s, cpu 10s) →
  some publishes carry no CPU observation and make no request for it (003 US-2/3).

## Out of scope

- **Windows as a platform omnistat runs on.** This feature specifies the `cpu` readings
  for Windows so the module is complete, but omnistat cannot run there until host
  identity (002 scopes Windows out) and service shutdown (003 FR-020 is built on
  SIGTERM) are specified, and until constitution V's build matrix is amended by ADR.
  That is its own feature.
- Per-core or per-CPU metrics, and the multi-entity publishing they would need.
- Per-state breakdown metrics (user / system / iowait / steal) — an additive follow-up:
  the counters are already read, only new manifest entries would be needed.
- CPU frequency, temperature, throttling, C-states, cache sizes, socket/physical-core
  topology, CPU flags.
- cgroup- or container-aware usage and quota accounting.
- Process-level CPU accounting.
- Migrating `hostname` or `machine-id` onto the new reading source — both already work
  on every platform through the standard library and are left alone.
- Alerting, thresholds, dashboards or automations built on these metrics.
- Changing how the core schedules, stamps, buffers or publishes (003, ADR-0005).

## Decisions taken during review (2026-09-22)

First round:

- Scope: aggregate usage + the three load averages as metrics; model, logical cores and
  architecture as dimensions. Per-state breakdown and per-core metrics deferred.
- First collection takes a bounded 250ms second sample so that one-shot `run` publishes a
  real number; providers may therefore hold state between calls — ADR-0006, extending
  ADR-0005 additively.
- Slugs: `load_avg_1/5/15` unprefixed — the names operators know from `uptime`, and load
  average is a run-queue measure rather than a CPU-time measure.
- Default interval 10s: six observations per default 60s publish; fine enough to show a
  spike and the first real exercise of the metric buffer.
- `cpu_arch` is a list of `amd64`/`arm64` — complete for every architecture omnistat
  ships a binary for (constitution V).

Second round, after researching cross-platform sources:

- The readings come from a cross-platform library rather than hand-written per-OS
  parsers — ADR-0008. This reverses the first round's "Linux only in v1": `usage` and the
  dimensions work on Linux, macOS and Windows from the start.
- Load average is **not** collected on Windows. The OS maintains none; the available
  substitute is a sampled processor-queue-length average, which is a different quantity
  and would be dishonest under these slugs (FR-006).
- Platform support is therefore declared **per attribute**, not per module — ADR-0007,
  which is how the feature turned out to need the granularity that ADR's own follow-up
  section had anticipated.
- Enabling cross-platform readings does **not** make omnistat run on Windows; identity
  and shutdown are unspecified there and are out of scope above.
- `hostname` stays on the standard library: `os.Hostname` already works on every target,
  so routing it through the new source would add a dependency to replace working code.

## Open questions

None.

## Implementation notes (2026-09-22)

Implemented per `plan.md`; `tasks.md` T001–T042 done. Deviations and findings:

- **Metric attributes need no special handling.** The existing `createAttribute` path
  works unchanged; the attribute reads back from discovery as `"type": "metric"`. This
  was the largest unknown going in (no metric attribute had ever been created against
  the real API) and it cost nothing.
- **`GetEntityChart` takes epoch seconds, typed `int32` in the SDK.** Milliseconds are
  not rejected — they return `200` with an empty series, which is a silent trap. The
  `int32` means the endpoint cannot address a time past January 2038; `epochSeconds`
  returns an error rather than wrapping.
- **Default bucket width is `1 hour`**, so a short acceptance run collapses into one
  point. The sandbox test asks for `1 second`.
- **A pre-existing bug was found and fixed**: any `modules.<name>.enabled: false`
  failed with `config: override for unknown module "<name>"`, because a config block
  carrying only `enabled`/`interval` was recorded as a schema override and then
  resolved against the manifests that the switch had just removed. Spec 001 FR-006 and
  US-4/1 require disabling to work, so it is fixed with a regression test. FR-002 and
  US-6 of this spec depended on it.
- **FR-012's prime is visible in a one-shot run**: `run` takes ~250ms longer on its
  first (and only) collection. Acceptable and intended; the daemon pays it once per
  process.
- **`arch` alone does not make a collection** (FR-015): the architecture is a property
  of the build, not of the host, so it is excluded from the "did anything read"
  count — otherwise a host that could report nothing would look healthy.
- **Sandbox acceptance (local API, project "Omnistat Test", `.env`)**: `schema apply`
  created all seven attributes and both `cpu_arch` options; `run --daemon` with
  `modules.cpu.interval: 1s`, `publish.interval: 2s` published repeatedly, and
  `cpu_usage_pct` read back as **14 points with 14 distinct timestamps**, values
  between 9.4 and 12.8 — NFR-006 met, closing the gap 003 left open. On this host
  `run --dry-run` showed 16 cores, `13th Gen Intel(R) Core(TM) i7-1360P`, and load
  averages matching `uptime`. `make sandbox` runs `TestSandbox_CPU`.
- **One unreproduced flake**: on the very first sandbox run — the one that created the
  cpu schema while the other sandbox tests ran concurrently against the same local API
  — `TestSandbox_Resolve` failed once. It has passed on every fresh run since (four
  full-suite runs). Recorded rather than explained away.
- **Not verified on macOS or Windows.** Those readings are covered by unit tests
  against a faked `Reader` and by the six-target `make crosscheck` gate, exactly as
  NFR-006 says. No omnistat acceptance run exists on either platform.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md`
