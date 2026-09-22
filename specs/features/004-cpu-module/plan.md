---
feature: 004-cpu-module
status: done              # draft | approved | done
approved: 2026-09-22
spec: ./spec.md
created: 2026-09-22
depends_on: [001-module-schema-reconciliation, 002-host-identity, 003-run-loop-publisher]
---

# Plan: `cpu` module — the first metric provider

## Constitution check
- [x] Uses the SDK for all API access (II) — this feature adds no write path. The one new
  API call is a **read** for sandbox acceptance (`GetEntityChart`, NFR-006), in
  `internal/omni` like every other call. gopsutil never touches the network.
- [x] Every template/attribute lives in exactly one module manifest with default slugs (III) —
  all seven attributes are declared in `internal/module/cpu`'s manifest and nowhere else;
  the provider returns manifest **keys**, never slugs.
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV) — unchanged;
  `cpu`'s attributes flow through the existing `Desired` → `Diff` → `Apply` path.
- [x] No secret can reach a commit, a flag, or a log line (IV) — values at debug only
  (FR-024); no new secret exists. Raw readings are never logged.
- [x] Every mutation is covered by dry-run (IV) — unchanged; `run --dry-run` already routes
  the publisher to `Printer`, which now also lists platform skips (FR-023).
- [x] Every FR/NFR has a test strategy using a fake API (V) — see table. The reading source
  is faked by a `Reader` interface; the Omnismith API stays faked by `omnitest`.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI) — one new package (`internal/module/cpu`, FR-001) and one new dependency
  (`gopsutil`, ADR-0008, FR-005/007/009 cross-platform). `cpu` imports only `manifest` and
  `module`, like `hostname`.

## Technical context
- Go 1.26, `CGO_ENABLED=0`. SDK `github.com/omnismith-sdk/go v1.0.14` (unchanged).
- **New dependency**: `github.com/shirou/gopsutil/v4 v4.26.8` (ADR-0008). Verified to build
  `CGO_ENABLED=0` for `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, `windows/{amd64,arm64}`.
  Adds 9 modules (purego, go-ole, plan9stats, perfstat, go-sysconf, numcpus, wmi, `x/sys`).
- Readings used, all stateless: `cpu.Times(false)`, `load.Avg()`, `cpu.Counts(true)`,
  `cpu.Info()`. **`cpu.Percent` is forbidden** — package-level baseline seeded in `init()`
  (ADR-0006, ADR-0008).
- API operation added (read-only, sandbox acceptance only):
  | Operation | SDK call | Used for |
  |-----------|----------|----------|
  | `getEntityChart` | `EntityAPI.GetEntityChart(ctx, entityID).AttributeIds(csv).Start(int32).End(int32).Execute()` | NFR-006 read back `cpu_usage_pct` as a series |
- Existing seams reused unchanged: `collect.Sources` / `Scheduler` / `Buffer`,
  `publish.Publisher` / `Printer`, `schema.Resolved.ListItems` (for `cpu_arch`, 003 FR-012a),
  `config.Settings.Intervals` and `Modules` (no new config keys).

## Approach

### 1. Per-attribute platform support (`internal/manifest`, `internal/collect`) — FR-017…FR-023, ADR-0007

`manifest.Attribute` and `manifest.DesiredAttribute` each gain one field, carried through
`Resolve` untouched by overrides:

```go
// Platforms are the GOOS values on which this attribute can be collected.
// Empty means every platform.
Platforms []string
```

`manifest.Validate` rejects a platform outside `manifest.KnownPlatforms`
(`linux`, `darwin`, `windows`) — a typo must be a fatal startup error, not a
silent skip (001 FR-004's rule applied to the new field).

Enforcement lives in `collect.Sources`, which already owns "which modules produce what".
Its signature grows a `goos string` (explicit, so FR-017…FR-023 are testable on any
machine — NFR-005) and a second return value:

```go
func Sources(desired manifest.Desired, mods []module.Module,
             intervals map[string]time.Duration, goos string) ([]Source, []Skipped, error)

type Skipped struct {
    Module    string   // always set
    Key, Slug string   // empty ⇒ the whole module is skipped (FR-019)
    Platforms []string // where it *is* collectable, for the message
}
```

For each source: partition its desired attributes into `Attrs` (collectable here) and
`Unsupported map[string]bool` (declared but not collectable here). A source whose `Attrs`
ends up empty is not returned at all and yields a module-level `Skipped` (FR-019).
Crucially, `Desired` is **not** filtered — the schema keeps every attribute on every
platform (FR-020), so `schema.Diff`/`Apply` are untouched.

`collectOne` gains three lines: an observation whose key is in `Unsupported` is dropped at
**debug** level, not error (FR-016 — it is not an omission, it was reported at startup).
That is the safety net; the provider is expected not to produce it in the first place.

`run.go` logs each `Skipped` once after the existing `module scheduled` lines (FR-023), and
in dry-run also prints them to stdout, with a `"skipped"` array in the JSON document
(US-5/3). `schema.go` needs no change.

### 2. The `cpu` module (`internal/module/cpu`) — FR-001…FR-016

One new package, mirroring `hostname`'s shape. The reading source is declared **by its
consumer** (as `schema.API` and `publish.API` are, ADR-0003) and faked in tests:

```go
type Reader interface {
    Times(ctx context.Context) (Times, error) // cumulative, aggregate over all CPUs
    LoadAvg(ctx context.Context) (Load, error)
    Counts(ctx context.Context) (int, error)
    Model(ctx context.Context) (string, error)
}
type Times struct{ User, Nice, System, Idle, Iowait, Irq, Softirq, Steal float64 }
type Load  struct{ One, Five, Fifteen float64 }
```

`gopsutilReader` is the default implementation, in the same package, and is the only file
in the repository that imports gopsutil.

```go
type Module struct {
    Reader Reader
    GOOS   string
    Now    func() time.Time
    Sleep  func(ctx context.Context, d time.Duration) error // injectable ⇒ tests never sleep
    Prime  time.Duration                                    // default 250ms (FR-012)

    mu   sync.Mutex
    last *Times                                             // FR-011
}
```

`Manifest()` returns the seven attributes of FR-001. `usage`, `model`, `cores`, `arch`
declare no platforms (collectable everywhere); `load1/5/15` declare `{linux, darwin}`
(FR-018). `DefaultInterval()` = `10 * time.Second` (FR-004).

`Collect` builds observations independently so one failure costs one value (FR-015/016):

1. **`model`** — `Reader.Model`; trim, take the first line, omit if empty (FR-009).
2. **`cores`** — `Reader.Counts`; omit on error (FR-007).
3. **`arch`** — `runtime.GOARCH`, emitted as-is; an option the manifest does not declare is
   dropped downstream by `collect.Validate` (FR-008, 003 FR-002) — the module does not
   pre-filter, so the error surfaces where every other bad list value does.
4. **`load1/5/15`** — only when `m.GOOS` is in the attribute's platforms; `Reader.LoadAvg`,
   values as-is, no arithmetic (FR-006).
5. **`usage`** — §3 below.
6. If the result is empty, return an error → an ordinary provider failure (FR-015, 003 FR-010).

Every omission appends to a per-call slice logged once as a single `slog` record with the
keys omitted and their reasons (FR-016).

### 3. Usage arithmetic — FR-005, FR-011…FR-014, ADR-0006

```
total = User+Nice+System+Idle+Iowait+Irq+Softirq+Steal
idle  = Idle + Iowait
usage = clamp((Δtotal - Δidle) / Δtotal * 100, 0, 100)
```

`Guest`/`GuestNice` are deliberately excluded from `Times`: the Linux kernel already counts
guest time inside `User` and guest-nice inside `Nice`, so including them double-counts.
`Iowait` is absent (zero) on Windows, which the formula handles without a special case.

Sequence, under `m.mu` so FR-014 holds:

```
cur ← Reader.Times                      ─ error ⇒ omit usage, keep m.last untouched
if m.last == nil:                        (first collection, FR-012)
    if deadline set and time until it < Prime + slack(50ms):
        m.last = cur ; omit usage        (US-2/2)
    else:
        Sleep(ctx, Prime)                ─ ctx done ⇒ m.last = cur ; omit usage
        cur2 ← Reader.Times              ─ error   ⇒ m.last = cur ; omit usage
        usage = pct(cur, cur2) ; m.last = cur2
else:
    usage = pct(*m.last, cur) ; m.last = cur
```

`pct` returns `(value, ok)`: `ok == false` when `Δtotal <= 0` or any counter decreased —
the reading still becomes the new baseline and no observation is produced (FR-013). The
baseline is assigned exactly once per branch, so a cancellation leaves it consistent
(FR-014).

With a 10s interval the steady-state path never sleeps; the 250ms pause happens once per
process (ADR-0006).

### 4. Registration & fixture hygiene

`cmd/omnistat/main.go` registers `cpu.New()` enabled by default (FR-002). No config change
is needed: `modules.cpu.{enabled,interval,template,attributes.*}` already work generically
(001 FR-006…008, 003 FR-003).

`moduletest.CPU()` is a *fixture* module also named `cpu`, with slugs `cpu_model`,
`cpu_usage_pct`, `cpu_arch`. It never ships, so 001 FR-002 is not violated today — but
`Registry.Register` panics on a duplicate name, and `internal/cli/{identity,run,sandbox}_test.go`
build production-like registries that will now contain the real module. Rename the fixture
to `moduletest.Probe()` (module `probe`, slugs `probe_*`) across its ~15 call sites, freeing
the name and the slugs.

### 5. Sandbox acceptance — NFR-006

`internal/omni` gains one read method, `EntityChart(ctx, entityID string, attributeIDs
[]string, start, end time.Time) (map[string][]Point, error)`, wrapping `GetEntityChart`.
`internal/cli/sandbox_test.go` gains `TestSandbox_CPU` (build tag `sandbox`): register the
real `machine-id`, `hostname` and `cpu` modules, `schema apply`, `run --daemon` for ~3
publish intervals against the real project, then read `cpu_usage_pct` back and assert more
than one point with distinct timestamps. This closes the gap 003 left open — the metric
path has so far only ever been exercised against `omnitest`.

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/module/cpu` | Manifest, provider, rate arithmetic, `Reader` + gopsutil impl | FR-001…FR-016 |
| `internal/manifest` (extend) | `Platforms` on `Attribute`/`DesiredAttribute`, `KnownPlatforms`, validation | FR-017, FR-018 |
| `internal/collect` (extend) | `Sources` takes `goos`, returns `[]Skipped`; `Unsupported` on `Source` | FR-019, FR-021, FR-022 |
| `internal/cli` (extend) | Log + dry-run print of skips | FR-023 |
| `internal/publish` (extend) | `"skipped"` in the dry-run JSON document | FR-023, US-5/3 |
| `internal/omni` (extend) | `EntityChart` (read-only) | NFR-006 |
| `internal/module/moduletest` (rename) | `CPU()` → `Probe()`, slugs `probe_*` | hygiene, §4 |
| `cmd/omnistat` (extend) | Register `cpu` | FR-002 |

Deliberately **not** created: a shared `internal/hostread` package. One consumer does not
justify the abstraction (constitution VI); extract it when `memory` or `disk` becomes the
second — see Risks.

## Data flow
```
manifest(cpu){7 attrs, per-attr Platforms} ─► Desired (ALL attrs, every platform) ─► Diff/Apply
                                                └► Resolved{ids, cpu_arch item ids}
collect.Sources(desired, mods, intervals, GOOS)
      ├─► Source{cpu, interval 10s, Attrs=collectable, Unsupported=rest}
      └─► []Skipped ─► startup log (FR-023) + dry-run stdout/JSON

per tick (ctx deadline = 10s):
  Reader.Model/Counts/LoadAvg/Times ─► observations by manifest KEY
  usage: last==nil ? (Times, sleep 250ms, Times) : (last, Times)   [mu, FR-011…014]
      ─► collect.Validate(kind) ─► Sample{slug, kind, value, At=clock.Now()}
      ─► Buffer: cpu_usage_pct + load_avg_* append (bound 5 000); model/cores/arch latest-wins

per publish.interval (60s):  1 × PATCH /entities/{id}   (model, cores, arch → arch as item id)
                             1 × POST  /entities/{id}/metrics  (~6 usage + ~6×3 load = 24 obs)
```
Request count is unchanged by this feature: `1 + ceil(n/1000)` per publish (003 NFR-003),
i.e. two requests per minute at defaults regardless of the 10s sampling.

## Configuration
No new settings. Existing generic keys apply:

| Setting | YAML | Default | Requirement |
|---------|------|---------|-------------|
| Enable/disable | `modules.cpu.enabled` | `true` | FR-002 |
| Collection interval | `modules.cpu.interval` | `10s` (1s…24h) | FR-004 |
| Slug / template / name overrides | `modules.cpu.attributes.<key>.*`, `modules.cpu.template` | manifest defaults | FR-003 |

Keys are `usage`, `load1`, `load5`, `load15`, `model`, `cores`, `arch`.

## Testing strategy
| Requirement | Test type | Where |
|-------------|-----------|-------|
| FR-001/002/004 manifest shape, slugs, kinds, options, default interval | unit, table | `internal/module/cpu/cpu_test.go` |
| FR-003 slug/template overrides reach the provider's attrs | unit | `internal/manifest/resolve_test.go` |
| FR-005 arithmetic: idle-only ⇒ 0, all-busy ⇒ 100, 8 cores ⇒ aggregate, guest not double-counted, clamping | unit, table, fake `Reader` | `internal/module/cpu/usage_test.go` |
| FR-006 load averages forwarded byte-for-byte; never synthesised | unit | `internal/module/cpu/cpu_test.go` |
| FR-007/009 counts; model trimmed, first line, empty ⇒ omitted | unit | same |
| FR-008 `riscv64` ⇒ observation dropped, collection still succeeds | unit + `collect.Validate` | `internal/collect/validate_test.go` |
| FR-010 dimensions re-observed every collection | unit, two calls | `internal/module/cpu/cpu_test.go` |
| FR-011 second call uses the stored reading and does not sleep | unit, fake `Sleep` asserts not called | `internal/module/cpu/usage_test.go` |
| FR-012 first call primes (sleep called once, usage present); tight deadline ⇒ no usage, dims kept | unit, fake `Sleep`/ctx | same |
| FR-013 Δtotal == 0 and counter-went-backwards ⇒ no usage, baseline advanced, no NaN/Inf | unit, table | same |
| FR-014 cancellation mid-prime leaves baseline consistent; concurrent Collect safe | unit + `-race` | `internal/module/cpu/usage_race_test.go` |
| FR-015/016 each source failing alone; all failing ⇒ provider error; omissions logged once | unit, fake `Reader` per-method errors | `internal/module/cpu/cpu_test.go` |
| FR-017 unknown platform in a manifest ⇒ fatal validation error | unit | `internal/manifest/validate_test.go` |
| FR-018/019 `goos=windows` ⇒ no load sources, `Skipped` for 3 keys; all-unsupported module not scheduled | unit, `Sources(…, "windows")` | `internal/collect/scheduler_test.go` |
| FR-020 `Desired` identical for linux/darwin/windows ⇒ same plan | unit | `internal/manifest/resolve_test.go` + `internal/schema/diff_test.go` |
| FR-021/022 disabled ≠ unsupported in log and dry-run; enabling an unsupported module skips, not errors | CLI test with omnitest | `internal/cli/run_test.go` |
| FR-023 startup lists interval + skips once; dry-run JSON carries `skipped` | CLI test on slog handler + stdout | same |
| FR-024 values only at debug; no raw readings in any record | unit on slog handler | same |
| NFR-001 one collection allocates boundedly | benchmark, informational | `internal/module/cpu/usage_test.go` |
| NFR-002 exactly one retained reading, size independent of CPU count | unit | same |
| NFR-003 request count unchanged with cpu enabled (2 per publish at defaults) | integration (omnitest) | `internal/cli/run_test.go` |
| NFR-004 no external command, no privilege | review + `Reader` interface has no exec | — |
| NFR-005 cross-compile gate for all 6 pairs with `CGO_ENABLED=0` | `make` target in CI | `Makefile`, `.github/workflows/ci.yml` |
| NFR-006 metrics ingested and read back as a series | sandbox (manual, `make sandbox`) | `internal/cli/sandbox_test.go` |

## Risks & unknowns
- **`GetEntityChart` parameter semantics**: `Start`/`End` are `int32` in the SDK, which
  suggests epoch **seconds** but is undocumented, and `AttributeIds` is a comma-separated
  string. Only the sandbox test depends on this; if the read-back proves unworkable,
  NFR-006 falls back to asserting the 202s plus a manual check in the UI, and the finding
  goes into `docs/reference/omnismith-api-notes.md`.
- **Metric attribute creation**: no metric attribute has ever been created against the real
  API (001's acceptance had no attributes, 003's was `text`). `attribute_type = 1` with
  numeric `data_type` is from the API notes, not from experience. Expect to learn something in the
  T003 spike; that is why it is scheduled first rather than folded into T040.
- **Ingest 422 field keys** are still undocumented, so a rejected chunk is dropped whole
  (003 implementation note). With a real metric attribute we may finally see the shape;
  record it either way.
- **gopsutil on macOS/Windows is unverified by us** — it compiles, and its readings are
  covered by upstream tests, but no omnistat acceptance run exists on those platforms. The
  `Reader` interface keeps the blast radius to one file per platform.
- **`Times` field drift**: if gopsutil adds or renames a state, our `Times` struct is a
  manual mapping and would silently miss it. Mitigated by the total/idle formula depending
  only on the states we name.
- **Fixture rename churn** touches ~15 call sites across three packages; mechanical, but it
  is the change most likely to produce a noisy diff.

## Decisions taken here that deserve an ADR
- Already recorded: ADR-0006 (rate providers), ADR-0007 (per-attribute platform support),
  ADR-0008 (gopsutil). Nothing new.
- Not an ADR, but recorded here: `Guest`/`GuestNice` are excluded from the usage
  denominator because Linux already counts them inside `User`/`Nice`. If a platform is ever
  found where that is untrue, it becomes a per-platform note in `Reader`, not a formula
  change.
