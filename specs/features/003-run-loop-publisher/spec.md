---
feature: 003-run-loop-publisher
status: implemented       # draft | review | approved | implemented | superseded
approved: 2026-09-22
implemented: 2026-09-22
created: 2026-09-22
owners: [evgenii]
supersedes: null
adr: [0001, 0005]
depends_on: [001-module-schema-reconciliation, 002-host-identity]
---

# Feature: Run loop, publisher & `hostname` module

## Summary

Features 001 and 002 make a project ready for data and find the host entity; nothing
yet writes a value. This feature closes the loop: `omnistat run` reconciles the schema,
resolves the host entity, collects values from every enabled module and publishes them
— dimensions as entity attributes, metrics as time-series observations. It runs once by
default and as a long-lived daemon on request. It fixes the contract every value module
follows from now on: a module declares *what* it measures (manifest, 001), *how often*
by default, and produces values *on demand*; the core owns the clock, the buffer and
the network. The `hostname` module ships as the first, reference provider: one text
dimension that gives the host entity its human-readable label (promised by 002 FR-014).

## Users & context

- **Operator** — installs omnistat as a system service (systemd, launchd) on hosts and
  expects it to keep the host entity current without attention: values arrive on a
  predictable cadence, a crash or restart costs at most a few seconds of data, and a
  misbehaving module never takes the whole agent down. Also runs it by hand, once, from
  a shell or a cron job, and wants to see what *would* be sent before it is.
- **Module author** — adds a new data source by writing a manifest and a value
  provider; wants to declare a sensible default cadence and otherwise never think about
  timers, buffering, retries or the API.
- **Platform owner** — runs the Omnismith project many hosts report into; wants
  ingestion to be batched and bounded so a fleet does not turn into a request storm.
- Runs on Linux (primary) and macOS, as root or an unprivileged user, in a VM, on bare
  metal or in a container. The network may be flaky; the API may be down for minutes.

## User stories

### US-1 — One shot: collect, publish, exit (P1)
As an operator, I want a single command that publishes the current state of this host
and exits, so that I can use omnistat from a shell, a cron job or a provisioning script.

**Acceptance scenarios**
1. **Given** a reconciled project and an existing host entity, **When** I run the run
   command with no mode flag, **Then** every enabled module is asked for its values
   exactly once, the dimensions are written to the host entity, the metric observations
   are ingested, the command reports what it published, and it exits with status 0.
2. **Given** an empty project and no host entity, **When** I run it, **Then** the schema
   is created, the host entity is created, and the values are published in that order,
   in the same invocation.
3. **Given** one module whose provider fails, **When** I run it, **Then** the values of
   the other modules are still published, the failure is reported with the module name
   and reason, and the exit status tells a script that the run was partial.

### US-2 — Daemon: keep the host current (P1)
As an operator, I want omnistat to run as a service that collects each module on its own
cadence and publishes on a fixed interval, so that dashboards and automations in
Omnismith see fresh data with a bounded, predictable request rate.

**Acceptance scenarios**
1. **Given** the daemon flag and a publish interval of 60s, **When** the daemon starts,
   **Then** it reconciles and resolves identity once, collects every module once, publishes
   as soon as that first round completes, and then publishes every 60s thereafter —
   never more often, regardless of how many modules there are or how fast they collect.
2. **Given** a module with a 10s collection interval and a 60s publish interval, **When**
   one publish happens, **Then** it carries all ~6 observations collected since the previous
   publish, each with the timestamp at which it was collected, not the time it was sent.
3. **Given** a module with a 5m collection interval and a 60s publish interval, **When**
   nothing new has been collected since the last publish, **Then** no request is made.
4. **Given** a running daemon, **When** it receives SIGTERM or SIGINT, **Then** it stops
   collecting, publishes whatever is buffered (within the HTTP deadline), and exits 0.

### US-3 — Survive the network, not the operator's patience (P1)
As an operator, I want a flaky API to cost me nothing but delay, so that an outage does
not lose observations or crash the agent.

**Acceptance scenarios**
1. **Given** the API returns 503 for five minutes, **When** the daemon keeps running,
   **Then** observations keep accumulating in memory with their collection timestamps,
   each publish attempt is retried with bounded backoff, and once the API recovers the
   backlog is published with the original timestamps.
2. **Given** the API stays down long enough for a metric's buffer to reach its bound,
   **When** new observations arrive, **Then** the oldest are dropped, the drop is logged
   with counts, and memory stays bounded.
3. **Given** the host entity was deleted in the project while the daemon runs, **When**
   the next publish fails with "not found", **Then** the daemon exits non-zero with a
   message naming the entity, so that the service manager restarts it and the next run
   recreates the entity (002 US-1).

### US-4 — See before you send (P2)
As an operator, I want a dry-run that shows exactly which values would be published,
so that I can check a new module or a remapped slug without writing anything.

**Acceptance scenarios**
1. **Given** any configuration, **When** I run with the dry-run flag, **Then** the
   collected values are printed per module and attribute, with their timestamps and
   the slug they would be written to, and no entity is written and no metric ingested.
2. **Given** the dry-run and daemon flags together, **When** the daemon runs, **Then** it
   prints what each publish tick would send, forever, writing nothing.

### US-5 — A module author adds a provider without touching the core (P2)
As a module author, I want to implement "give me your values now" and declare a default
interval, so that scheduling, buffering, timestamping and the API stay someone else's
problem.

**Acceptance scenarios**
1. **Given** a module that declares attributes and a provider, **When** it is enabled,
   **Then** the core calls it on its interval, stamps and buffers what it returns, and
   publishes it — the module never sees a timer, a buffer or the API.
2. **Given** a provider that returns a value for an attribute its manifest does not
   declare, or a list value not among the declared options, **When** it is collected,
   **Then** that value is dropped with an error naming the module and key; the rest of
   the collection is kept.

### US-6 — The host has a name (P2)
As an operator, I want each host entity to show the machine's hostname, so that I can
recognise hosts in Omnismith (the identity is an opaque token, 002 US-4).

**Acceptance scenarios**
1. **Given** the default module set, **When** omnistat runs, **Then** the host entity's
   `hostname` attribute equals the operating system's hostname.
2. **Given** the daemon and a hostname change, **When** the next hostname collection
   happens, **Then** the new value is published and the entity's history shows the change.

## Functional requirements

### Provider contract
- **FR-001** A module MAY supply a provider. A provider, when asked, MUST return a
  *collection*: zero or more observations, each `(attribute key, value)` for a key the
  module's manifest declares. A module without a provider (e.g. one that only declares
  schema) contributes nothing to the loop.
- **FR-002** Values MUST be typed by the attribute's manifest kind: `text` → string;
  `number` → number; `boolean` → boolean; `date` → calendar date; `datetime` → instant;
  `list` → the human-readable value of one of the manifest's declared options (the
  provider never sees platform identifiers; the core maps the option to the list
  item's identifier at publish time, FR-012a); `metric` → number. A value of the wrong
  type, an undeclared key, or an undeclared list option MUST be dropped with an error
  log naming module and key; other observations in the same collection are kept.
- **FR-003** A provider MUST declare a *default collection interval*. The operator MUST
  be able to override it per module in config. Intervals MUST be at least 1s and at most
  24h; a value outside that range is a fatal configuration error before any network call.
- **FR-004** Providers MUST be called by the core, on demand, and MUST NOT own timers,
  goroutines that outlive a call, buffers or any network access (ADR-0005). A call MUST
  carry a deadline; a collection that has not returned by its next scheduled tick is
  cancelled and counts as failed for that tick.
- **FR-005** The `machine-id` module's provider role is identity only (002); it MUST NOT
  produce periodic values and its attribute is never re-published by the loop.

### Collection & buffering
- **FR-006** Every observation MUST be stamped by the core with the instant the provider
  returned it (UTC). This stamp is the observation time sent to the platform; it is never
  the publish time.
- **FR-007** The core MUST keep an in-memory buffer between publishes. For a `metric`
  attribute every observation is kept, in collection order. For any other kind only the
  latest observation per attribute is kept (an earlier one still unpublished is replaced).
- **FR-008** The metric buffer MUST be bounded to **5 000 observations per attribute**.
  When full, the oldest observation is dropped for each new one, and a warning with the
  attribute and the number dropped is logged at most once per publish interval.
- **FR-009** Buffered observations MUST survive a failed publish: they are removed only
  once the platform has accepted them.
- **FR-010** A provider failure (error or deadline) MUST NOT stop the loop or affect
  other modules: the failure is logged with module and reason, the tick is skipped, and
  the module is called again at its next tick.
- **FR-011** Collections of the same module MUST NOT overlap; collections of different
  modules MAY run concurrently.

### Publishing
- **FR-012** A publish MUST send everything buffered for the host entity in at most two
  kinds of write: one dimension update carrying every buffered non-metric observation as a
  backfilled value with its FR-006 timestamp, and one or more metric ingestions carrying
  the buffered observations in chunks of at most 1 000, each with its FR-006 timestamp.
  A publish with an empty buffer MUST make no request.
- **FR-012a** A `list` observation MUST be published as the identifier of the list item
  whose value equals the observed option (exact, case-sensitive — 001 FR-016), taken
  from the identifiers resolved at startup (001 FR-025). An option that has no item in
  the project (schema drifted after startup) MUST be dropped with an error log, not
  sent as text.
- **FR-013** The publish interval MUST be configurable, default **60s**, minimum 1s,
  maximum 1h; outside that range is a fatal configuration error.
- **FR-014** In daemon mode the first publish MUST happen as soon as every enabled
  provider has completed (or failed) its first collection; later publishes happen every
  publish interval. Publishes MUST NOT overlap: if one is still running when the next
  tick is due, that tick is skipped.
- **FR-015** Transient publish failures (timeouts, 5xx, 429) MUST be retried with the
  bounded, jittered policy of 001 NFR-003, then left in the buffer for the next tick.
  A `not found` response for the host entity MUST end the run with a non-zero exit
  status naming the entity (US-3/3). A validation error (422) MUST be logged with the
  platform's field errors and the offending observations dropped; it is never retried
  with the same payload.
- **FR-016** Dimensions MUST be sent as collected, without local change detection; the
  platform records unchanged values at no cost.
- **FR-017** Metric ingestion is accepted asynchronously by the platform; acceptance
  (HTTP 202) counts as delivered. Delivery is at-least-once (constitution IV).

### Run modes & command
- **FR-018** `omnistat run` MUST execute, in order: reconciliation in the configured mode
  (001 FR-010), identity resolution (002 FR-016), one collection of every enabled module,
  one publish, then exit. Exit status: 0 when every provider succeeded and everything
  was published; a distinct documented status when the run was partial (some provider
  failed but the rest was published); non-zero otherwise.
- **FR-019** A daemon flag MUST switch `run` into a long-lived loop: reconciliation and
  identity resolution once at start, then FR-014's schedule until a stop signal. The
  daemon MUST NOT re-reconcile or re-resolve during its life.
- **FR-020** On SIGINT/SIGTERM the daemon MUST stop scheduling, attempt one final publish
  bounded by the HTTP timeout, and exit 0. A second signal during the final publish
  MUST exit immediately. On Windows, a stop, shutdown or preshutdown request from the
  service manager, and Ctrl+C, Ctrl+Break or closing the console, have the same effect
  (spec 006 FR-021–FR-024).
- **FR-021** A dry-run flag MUST make `run` (in either mode) perform every step except
  the writes: the schema plan is shown instead of applied, entity creation is reported
  instead of performed, and each publish prints module, attribute key, target slug,
  timestamp and value of everything that would be sent. Dry-run MUST be able to emit
  JSON (carrying a `version` field) on request. A `list` value whose option the shown
  schema plan would create has no item id yet. It MUST be printed with its value and
  marked as created by the schema apply (`pending_option` in JSON). It MUST NOT be
  dropped with an error: once the plan is applied, it would be sent. An option that is
  neither in the project nor in the plan is still dropped with an error (FR-012a).
  *(Amended 2026-09-25: on an empty project the dry-run logged `observation dropped` for
  `cpu_arch`, found during spec 006 acceptance.)*
- **FR-022** Reconciliation mode `off` with a missing schema MUST fail at the first use
  with 001's missing-list message, before any collection.

### `hostname` module
- **FR-023** The `hostname` module MUST declare one attribute: kind `text`, default slug
  `hostname`, human name "Hostname", attached to the host template. It is enabled by
  default and may be disabled.
- **FR-024** Its provider MUST return the hostname reported by the operating system,
  trimmed; an empty value is a provider failure (FR-010). No DNS lookup is performed.
- **FR-025** Its default collection interval MUST be 5m.

### Observability
- **FR-026** Every publish MUST log, structurally: number of dimensions and metric
  observations sent, number of requests, duration, and outcome. Every dropped observation
  (FR-002, FR-008, FR-015) MUST be logged with its cause. Values themselves are logged at
  debug level only. No log line contains the token or the project id.
- **FR-026a** How publishes are logged depends on the mode:
  - **One-shot run:** its publish is logged at info.
  - **Daemon:** a successful publish is logged at debug. At info, the daemon logs:
    - the first successful publish after start;
    - the first success after one or more failed publishes, with the number of failures;
    - a **summary** every `log.summary_interval`. The summary carries the period and,
      over it: the number of publishes, the dimensions, observations and requests they
      sent, the dropped observations, the failed publishes and the longest duration. It
      is logged even when nothing was published in the period, so it doubles as a
      heartbeat.

  A failed publish is logged at error when it happens, in both modes. On stop, the
  summary of the unfinished period is logged before `stopped`. `log.summary_interval`
  defaults to **15m**; `0` logs every publish at info, as before. Any other value must
  be between 1m and 24h.
  *(Added 2026-09-25: one info line per publish, 1 440 a day at the default interval,
  crowded out other applications' events in the Windows Application log. Found during
  spec 006 acceptance.)*
- **FR-027** Startup MUST log the effective schedule: each enabled module with its
  collection interval, and the publish interval.

## Non-functional requirements
- **NFR-001** (reliability) No collection or publish runs without a deadline; the daemon
  never blocks indefinitely on the network or on a provider.
- **NFR-002** (resources) Memory used by buffers is bounded by FR-007/FR-008 and the
  number of declared attributes; a daemon that cannot reach the API for a day does not
  grow beyond that bound. No goroutine is leaked across ticks.
- **NFR-003** (network) A daemon with N enabled modules makes at most
  `1 + ceil(observations / 1 000)` requests per publish interval, independent of N and
  of the modules' collection intervals.
- **NFR-004** (safety) The loop uses only additive writes: entity update and metric
  ingestion. No delete, replace or schema call (constitution IV, ADR-0003).
- **NFR-005** (shutdown) After a stop signal the process exits within the HTTP timeout
  plus one second.
- **NFR-006** (testability) Scheduling, stamping and buffering are deterministic under an
  injected clock and run identically in `go test` without network; the publisher is
  tested against the fake API.

## Data & integration contract

Read: nothing beyond 001 (schema, including list option identifiers) and 002 (host
entity). Every write targets the one host entity resolved by 002.

Write (additive only):
- **Dimensions** — partial entity update on the host entity: `{ slug: { value, updated_at } }`
  for every buffered non-metric observation, keyed by the attribute's resolved slug;
  `list` values are sent as the list item's identifier, never its label (FR-012a).
  Kinds map as 001's domain mapping.
- **Metrics** — metric ingestion on the host entity: `metric_values: [{ attribute_slug,
  value, updated_at }]`, values rendered as decimal strings, ≤ 1 000 per request.

Owned attributes: `hostname` (text, host template) — module `hostname`.
`machine_id` (module `machine-id`, 002) is read-only for this feature.

## Edge cases & failure modes

- API unreachable at start → reconciliation/resolution fail as in 001/002; `run` exits
  non-zero; nothing is collected.
- API unreachable mid-daemon → FR-009/FR-015: buffer, retry, backfill on recovery.
- 401 → exit non-zero (token invalid; a restart will not help without operator action).
  403 `stale_project_grant` → refresh once and retry (001). 404 host entity → FR-015 exit.
  422 → drop the named observations, keep running. 429 → retry policy.
- Provider panics → treated as a provider failure for that tick; the daemon survives.
- Provider slower than its interval → cancelled at the next tick, counted as failed,
  called again (no pile-up, FR-004/FR-011).
- Publish slower than the publish interval → the next tick is skipped (FR-014); the
  following one carries everything.
- Clock jumps backwards → collection timestamps may be non-monotonic; they are sent as
  observed. Scheduling uses the monotonic clock and is unaffected.
- Two daemons on the same host → both publish to the same entity (002 guarantees one
  entity); duplicate observations result. Not detected; the operator's service manager
  is expected to run one instance.
- Module disabled in config → not collected, its existing values untouched.
- Hostname unavailable (empty) → provider failure, logged; other modules unaffected.

## Out of scope

- Any value module other than `hostname` (`ip-address`, `cpu`, `memory`, … each get
  their own spec). The metric path is therefore proven against the fake API in this
  feature; the first metric module re-runs sandbox acceptance for it, as 002 did for 001.
- Provider-supplied observation timestamps (the core always stamps; revisit when a
  module has an inherent observation time).
- Persisting the buffer to disk across restarts.
- Per-platform module gating (constitution V) — comes with the first module that needs it.
- Health endpoints, self-metrics, a metrics port.
- Publishing to more than one entity, or to entities other than the host.
- Change detection or rate limiting of dimensions beyond FR-007.
- Configuring daemon mode from the config file (it is a flag by design: the unit file
  or crontab decides how omnistat runs, not the shared config).
- ~~Windows~~: specified by 006.

## Decisions taken during review (2026-09-22)

- `list` observations carry the option's human value; the core maps it to the list
  item id (FR-012a). Providers never handle platform identifiers.
- Scope: `hostname` is in (promised by 002 FR-014 and needed for observable acceptance);
  every other value module is its own spec.
- The core owns the clock; providers are on-demand, pure collect functions — ADR-0005.
- Timestamps: the core stamps at collection time; providers never do (for now).
- Buffer: metrics keep all, bounded at 5 000 per attribute (oldest dropped, warn);
  dimensions keep the latest only; loss on crash accepted; final publish on signal.
- Config keys: `publish.interval` (1s–1h, default 60s) and `modules.<name>.interval`
  (1s–24h, module default); daemon mode is a flag, not a config key.
- Host entity 404 in daemon mode → exit non-zero; the service manager restarts.
- Partial one-shot run → exit status 2 (same "attention needed, not an error" meaning
  `schema plan` gives it).
- Dry-run is allowed in daemon mode.

## Open questions

None.

## Implementation notes (2026-09-22)

Implemented per `plan.md`; `tasks.md` T001–T020 done. Deviations and findings:

- **Timestamps**: the platform rejects RFC 3339 with nanosecond precision (422
  `updated_at must be an RFC 3339 timestamp…`); it accepts up to microseconds. The
  SDK layer truncates every observation time to microseconds (FR-006 stamps are
  otherwise unchanged). Found in the sandbox run, not by the fake.
- **`schema.mode: off` in `run`** behaves like `verify`: the schema read cannot be
  skipped because FR-025 ids are needed to publish; only the reconcile is skipped.
- **Dimension numbers are sent as decimal strings** (the SDK's scalar union only
  offers `float32`); dates as `YYYY-MM-DD`, datetimes as RFC 3339 UTC, list values
  as item ids (FR-012a). Booleans go as booleans.
- **Config check before the network (FR-003)**: `run` validates sources (interval
  on a module without a provider) before its first request.
- Apply now re-reads the schema after adding list options (as it already did
  after binds) so the item ids of options created in the same run are known —
  one read more than 001 NFR-002's budget in that case only.
- 422 on a metric chunk drops the whole chunk (ingest field keys are undocumented);
  422 on the dimension update drops the named slugs and retries once (FR-015).
- Coinciding grids (e.g. cpu 10s, publish 60s): when a collection and a publish
  fall on the same instant the order is arbitrary and the sample lands on the next
  publish. Harmless; tests use non-coinciding grids.
- Sandbox acceptance (local API, project "Omnistat Test", `.env`): `schema plan`
  showed `+ attribute hostname`; `run --dry-run` printed the hostname with a stamp
  and wrote nothing; `run` created the attribute, the host entity and published
  `hostname` (read back through the API); `run --daemon` with `publish.interval: 5s`,
  `modules.hostname.interval: 2s` published right after the first collection, then
  every 5s; SIGTERM → final publish → `msg=stopped`, exit 0; `run --daemon --dry-run`
  printed one block per tick; `schema.mode: verify` with a remapped slug failed
  before collection with the missing list. `make sandbox` runs `TestSandbox_Run`.
  The metric path is proven against the fake API only (no metric module yet).

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md`
