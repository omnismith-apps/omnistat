---
feature: 003-run-loop-publisher
status: done              # draft | approved | done
approved: 2026-09-22
spec: ./spec.md
created: 2026-09-22
depends_on: [001-module-schema-reconciliation, 002-host-identity]
---

# Plan: Run loop, publisher & `hostname` module

## Constitution check
- [x] Uses the SDK for all API access (II) — two new `omni` methods (`UpdateEntity`,
  `IngestMetrics`), both SDK calls; `ReadSchema` additionally keeps list item ids.
- [x] Every template/attribute lives in exactly one module manifest with default slugs (III) —
  `hostname` lives in `internal/module/hostname`; the loop only ever sees slugs through
  `manifest.Desired` / `schema.Resolved`.
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV) — `publish.API`
  has exactly two methods: partial entity update and metric ingestion (ADR-0003).
- [x] No secret can reach a commit, a flag, or a log line (IV) — values are logged at debug
  only (FR-026); the client's logger never sees headers.
- [x] Every mutation is covered by dry-run (IV) — `run --dry-run` swaps the publisher's API for
  a printer; reconciliation and identity reuse their existing dry-run paths (FR-021).
- [x] Every FR/NFR has a test strategy using a fake API (V) — see table; `omnitest` gains the
  two entity write routes.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI) — two new core packages (`collect`, `publish`), one module package, no new
  dependencies. `hostname` imports only `manifest` and `module`.

## Technical context
- Go 1.26, `CGO_ENABLED=0`, linux/darwin. SDK `github.com/omnismith-sdk/go v1.0.14`.
- API operations:
  | Operation | SDK call | Used for |
  |-----------|----------|----------|
  | `updateEntity` | `EntityAPI.UpdateEntity(ctx, entityID).UpdateEntityRequest({attributes: {slug: {value, updated_at}}}).Execute()` | FR-012 dimensions (backfill object per attribute) |
  | `ingestEntityMetrics` | `EntityAPI.IngestEntityMetrics(ctx, entityID).IngestMetricsRequest({metric_values: [{attribute_slug, value, updated_at}]}).Execute()` → 202 | FR-012/017 metrics, ≤ 1 000 per call |
  | `getProjectSchema` | existing; now also reads `options[].id` | FR-012a list item ids |
- Value encoding (from `EntityAttributesInput` rules): every dimension is sent as a backfill
  object whose `value` is a **string** — text as-is, numbers via `strconv.FormatFloat(v, 'f', -1, 64)`
  (the SDK's scalar union only offers `float32`, which would round int64/float64), booleans
  `"true"/"false"`, date `YYYY-MM-DD`, datetime RFC 3339 UTC, list → item UUID. Metrics are
  strings by contract. `updated_at` is `time.Time` in UTC (marshals with `Z`).
- Existing seams reused: `cli.prepareWith` (config → modules → desired → client → one schema
  read), `schema.Apply`/`Diff`, `identity.Resolve`, `omni` transport retries (NFR-003 of 001),
  `signal.NotifyContext` in `main`.

## Approach

1. **Provider contract** (`internal/module`, FR-001…005, ADR-0005). Add
   ```go
   type Observation struct{ Key string; Value any }
   type Provider interface {
       Collect(ctx context.Context) ([]Observation, error)
       DefaultInterval() time.Duration
   }
   ```
   A module implements `Provider` optionally; the core discovers it by type assertion.
   `machineid.Module` does not implement it (FR-005). Providers get a context whose deadline
   is their interval (FR-004).

2. **`hostname` module** (`internal/module/hostname`, FR-023…025): manifest with one `text`
   attribute (key `hostname`, slug `hostname`, "Hostname"); `Collect` returns
   `strings.TrimSpace(hostname())` where `hostname` is injectable (`os.Hostname` by default);
   empty → error. `DefaultInterval` = 5m. Registered enabled-by-default in `main`.

3. **Collection, validation & buffer** (`internal/collect`, FR-002, FR-006…011, FR-014, NFR-002/006).
   Pure package, no SDK, injected `Clock` (`Now`, `NewTimer`) so tests advance time by hand.
   - `Source{Module string; Provider; Interval}` built by the CLI from the registry, the
     desired schema and config overrides; `Plan(desired, sources)` maps each `(module, key)`
     to its `DesiredAttribute` (slug, kind, options) once at startup.
   - `Validate(kind, options, value) (Value, error)` coerces to the canonical Go type per kind
     (`text`→string, `number`/`metric`→float64 from any int/float, `boolean`→bool,
     `date`/`datetime`→`time.Time`, `list`→string ∈ options) and rejects anything else (FR-002);
     unknown keys are rejected by `Plan`'s lookup. Rejections are logged with module/key and
     the rest of the collection continues.
   - `Sample{Module, Key, Slug, Kind, Value, At}`; `At = clock.Now().UTC()` when the provider
     returns (FR-006).
   - `Buffer`: `dims map[slug]Sample` (latest wins, FR-007) and `metrics map[slug][]Sample`
     bounded at `MaxPerMetric = 5000` (drop oldest, count drops per slug, FR-008).
     `Snapshot() Batch` copies what is pending and remembers per-slug cut points;
     `Ack(batch, dims []slug, metricsUpTo map[slug]int)` removes only what was accepted
     (FR-009 — a partially successful publish acks partially). `Drops()` returns and resets
     the drop counters so the loop can log them once per publish interval.
   - `Scheduler.Run(ctx)`: one goroutine per source; loop `collect → stamp → validate → buffer
     → wait interval`, so collections of one module never overlap (FR-011) and a failure only
     logs and waits for the next tick (FR-010). Each call runs under
     `context.WithTimeout(ctx, interval)` and a `recover()` that turns a panic into an error.
     `FirstRound() <-chan struct{}` closes when every source has finished its first attempt
     (FR-014).
   - `Once(ctx, sources)`: the one-shot path — every provider once, concurrently, same
     validation; returns which modules failed (FR-018 partial status).

4. **Publisher** (`internal/publish`, FR-012, FR-012a, FR-015…017, NFR-003/004).
   ```go
   type API interface {
       UpdateEntity(ctx, entityID string, attrs map[string]Backfill) error
       IngestMetrics(ctx, entityID string, obs []Metric) error
   }
   ```
   `Publisher{API, EntityID, ListItems map[slug]map[value]id, Log}`; `Publish(ctx, Batch)
   (Ack, error)`: build the dimension map (FR-012a mapping; unmapped option → drop + error
   log), one `UpdateEntity` if non-empty, then metric chunks of `ChunkSize = 1000`, each an
   `IngestMetrics`. Error handling per call:
   - `omni.ErrNotFound` → return `ErrEntityGone` (the loop exits non-zero, FR-015/US-3/3);
   - `omni.ErrValidation` on the dimension update → drop the slugs named in `Fields`
     (`attributes.<slug>`), log them, retry once without them; on a metric chunk → log and
     drop the chunk (a 422 there means the attribute itself is refused);
   - anything else (transport already retried 5xx/429/timeouts) → return the error with the
     partial `Ack` so the buffer keeps the rest.
   `Printer` implements `API` for dry-run: text (`module.key → slug = value @ ts`) or JSON
   (`{"version":1,"entity":…,"dimensions":[…],"metrics":[…]}`) to stdout, never fails.

5. **Run command** (`internal/cli/run.go`, FR-018…022, FR-026/027, NFR-001/005):
   `omnistat run [--daemon] [--dry-run] [--json]`.
   1. `prepare` (one schema read). Reconcile by mode: `apply` → show plan, `schema.Apply`
      (dry-run: show plan only, then proceed with `resolvedFrom(current)` — missing objects
      make the identity step report "would create"); `verify`/`off` → `Diff` must have no
      actions, else fail with the missing list (FR-022; `off` cannot skip the read because
      FR-025 ids are needed anyway — note in spec sync).
   2. Identity: reuse `resolveDryRun`'s target lookup (factor into `identityTarget(p)`), then
      `identity.Resolve(ctx, api, target, value, dryRun, log)`.
   3. Build sources from `Registry` (modules implementing `Provider`), intervals from config,
      `collect.Plan`. Log the schedule (FR-027).
   4. One-shot: `collect.Once` → `Publish` → exit `0` / `ExitPartial` (2, spec decision) /
      `1`. Daemon: `Scheduler.Run`; wait `FirstRound`; publish; then `for { select {
      case <-ticker.C: publish; log drops; case <-ctx.Done(): final publish under
      context.WithTimeout(context.Background(), s.HTTP.Timeout); return 0 } }`. The publish
      runs inline in the loop goroutine, so a slow publish naturally coalesces the next tick
      (FR-014). `ErrEntityGone` → return `ExitError` with the entity id.
   5. `main`: after the first signal cancels the context, call `stop()` so a second signal
      falls back to the default handler and terminates immediately (FR-020).

6. **Config** (`internal/config`, FR-003, FR-013): `publish.interval` (duration, 1s…1h,
   default 60s) and `modules.<name>.interval` (duration, 1s…24h, absent = provider default),
   validated at load; a module interval on a module without a provider is a config error
   ("module X produces no values").

7. **Client & fake** (`internal/omni`, `omnitest`): `UpdateEntity`/`IngestMetrics` with
   `mapErr`; `ReadSchema` fills `CurrentAttribute.OptionIDs map[value]id`, and
   `schema.resolvedFrom` fills `Resolved.ListItems`. `omnitest` routes `PATCH /entities/{id}`
   (stores values, honours backfill objects, 404 unknown id, 422 via `FailNext`) and
   `POST /entities/{id}/metrics` (appends to `metrics[entity][slug]`, 202, 404 unknown id;
   no size limit, since the platform's is undocumented); snapshots `EntityMetrics(id)` and
   `EntityValues(id)` for assertions.

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/module` (extend) | `Provider`, `Observation` | FR-001, FR-003, ADR-0005 |
| `internal/module/hostname` | Manifest + provider | FR-023…025 |
| `internal/collect` | Validation, stamping, bounded buffer, scheduler, one-shot | FR-002, FR-006…011, FR-014, NFR-002/006 |
| `internal/publish` | Batch → API calls, list mapping, 422/404 handling, dry-run printer | FR-012, FR-012a, FR-015…017, FR-021, NFR-003 |
| `internal/omni` (extend) | `UpdateEntity`, `IngestMetrics`, option ids in `ReadSchema` | FR-012, FR-012a |
| `internal/omni/omnitest` (extend) | Entity update/metrics routes, snapshots | tests |
| `internal/schema` (extend) | `OptionIDs`, `Resolved.ListItems` | FR-012a |
| `internal/config` (extend) | `publish.interval`, `modules.<name>.interval` | FR-003, FR-013 |
| `internal/cli` (extend) | `run` command, `ExitPartial` | FR-018…022, FR-026/027 |
| `cmd/omnistat` (extend) | register `hostname`; second-signal handling | FR-020, FR-023 |

## Data flow
```
config ─► registry.Enabled ─► manifests ─► Desired ─► ReadSchema ─► reconcile (apply|verify|off)
      └► Resolved{ids, list items}                                    │
identity.Discover ─► identity.Resolve ──────────────────────────────► EntityID
sources = modules implementing Provider × intervals(config)
daemon:  per source goroutine: Collect(ctx≤interval) → stamp(now) → Validate → Buffer
         loop: FirstRound → Publish; every publish.interval → Snapshot → Publish → Ack
one-shot: Once → Buffer → Publish → exit 0 | 2 (partial) | 1
Publish: dims → PATCH /entities/{id} {slug:{value,updated_at}}   (≤1 request)
         metrics → POST /entities/{id}/metrics chunks of ≤1000   (n requests)
         404 → ErrEntityGone → exit 1 · 422 → drop named · else keep in buffer, retry next tick
signal:  cancel scheduler → final Publish(timeout=http.timeout) → exit 0 · second signal → kill
```
Idempotency: the entity id is fixed for the run (002 FR-015); dimensions are last-write-wins per
attribute; metrics are at-least-once (a timeout after the platform accepted a chunk duplicates it —
accepted, constitution IV).

## Configuration
| Setting | YAML | Env | Flag | Default | Requirement |
|---------|------|-----|------|---------|-------------|
| Publish interval | `publish.interval` | — | — | `60s` (1s…1h) | FR-013 |
| Module collection interval | `modules.<name>.interval` | — | — | provider default (1s…24h) | FR-003 |
| Daemon mode | — | — | `run --daemon` | off | FR-019 |
| Dry-run | — | — | `run --dry-run` | off | FR-021 |
| Dry-run as JSON | — | — | `run --json` (requires `--dry-run`) | off | FR-021 |
| HTTP timeout (final publish bound) | `http.timeout` (existing) | — | — | 15s | FR-020, NFR-005 |

## Testing strategy
| Requirement | Test type | Where |
|-------------|-----------|-------|
| FR-001/004 provider called with deadline ≤ interval, no overlap, panic → error | unit, fake clock | `internal/collect/scheduler_test.go` |
| FR-002 typing per kind, undeclared key, bad list option dropped, rest kept | unit, table | `internal/collect/validate_test.go` |
| FR-003/013 interval ranges, module-without-provider error | unit | `internal/config/config_test.go` |
| FR-005 machine-id has no provider | unit | `internal/module/machineid/machineid_test.go` |
| FR-006 stamp = clock at return, not publish | unit, fake clock | `internal/collect/scheduler_test.go` |
| FR-007/008/009 latest-dim, metric bound 5 000, drop counters, partial ack | unit | `internal/collect/buffer_test.go` |
| FR-010/011 failing module isolated; per-module serial, cross-module concurrent | unit + `-race` | `internal/collect/scheduler_race_test.go` |
| FR-012/012a payload shapes, chunking at 1 000, list → item id, empty batch → 0 requests | integration (omnitest) | `internal/publish/publish_test.go` |
| FR-014 first publish after first round; tick coalescing when publish is slow | unit, fake clock | `internal/cli/run_test.go` (loop extracted as `runDaemon(deps)`) |
| FR-015 404 → ErrEntityGone; 422 dims retry-once-without; 422 chunk dropped; 503 keeps buffer | integration (omnitest `FailNext`) | `internal/publish/publish_test.go` |
| FR-016 unchanged dims still sent | integration | same |
| FR-017 202 counts as acked | integration | same |
| FR-018 exit 0 / 2 / 1; order reconcile → resolve → collect → publish | CLI test with omnitest | `internal/cli/run_test.go` |
| FR-019/020 daemon: no re-reconcile; SIGTERM → final publish → exit 0 | CLI test, cancel ctx | `internal/cli/run_test.go` |
| FR-021 dry-run: zero write requests, text + JSON output, daemon dry-run | CLI test | `internal/cli/run_test.go` |
| FR-022 mode off/verify with missing schema fails before collect | CLI test | same |
| FR-023…025 manifest, trim, empty → error, 5m | unit | `internal/module/hostname/hostname_test.go` |
| FR-026/027 log fields present, values only at debug | unit on slog handler | `internal/cli/run_test.go` |
| NFR-002 no goroutine leak after stop | unit (`runtime.NumGoroutine` before/after) | `internal/collect/scheduler_test.go` |
| NFR-003 request count = 1 + ceil(n/1000) | integration | `internal/publish/publish_test.go` |
| NFR-005 final publish bounded | CLI test with stalled omnitest | `internal/cli/run_test.go` |
| Sandbox (manual, `make sandbox`) | `TestSandbox_Run`: one-shot against the real project, asserts `hostname` on the host entity via search | `internal/cli/sandbox_test.go` |

## Risks & unknowns
- **`updated_at` format**: the SDK marshals `time.Time` as RFC 3339 with `Z`; the platform
  requires an explicit offset — should be fine, verified in the sandbox run.
- **Metric payload size limit** is undocumented; 1 000 per request is our own bound. If the
  platform rejects it with 413/400, the fix is a smaller constant, not a design change.
- **Discovery and 422 field keys** for metric ingestion are undocumented; the plan drops the
  chunk rather than parsing keys. Revisit when the first metric module lands.
- **Ticker drift**: `time.Ticker` drops ticks when the receiver is slow — desired (FR-014).
- The real build has no metric attribute, so the metric path is proven with omnitest only
  (spec "Out of scope"); `cpu` (004) re-runs sandbox acceptance for it.

## Decisions taken here that deserve an ADR
- None beyond ADR-0005 (already accepted). The "numbers as strings" encoding is an
  implementation detail of `omni`, documented in `docs/reference/omnismith-api-notes.md`.
