---
feature: 005-memory-module
status: done              # draft | approved | done
approved: 2026-09-24
spec: ./spec.md
created: 2026-09-24
depends_on: [004-cpu-module]
---

# Plan: `memory` module — how close a host is to swapping

## Constitution check
- [x] Uses the SDK for all API access (II). No new write path. One new **read** for
  sandbox acceptance (`getEntity`, NFR-005), in `internal/omni` like every other call.
- [x] Every template/attribute lives in exactly one module manifest with default slugs (III).
  The three attributes are declared in `internal/module/memory`'s manifest only. The
  provider returns manifest **keys**, never slugs.
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV). Unchanged.
- [x] No secret can reach a commit, a flag, or a log line (IV). Values are logged at debug
  only (FR-015), and no new secret exists.
- [x] Every mutation is covered by dry-run (IV). Unchanged; `run --dry-run` already prints
  values and platform skips.
- [x] Every FR/NFR has a test strategy using a fake API (V). See the table below. Host
  readings are faked through `memory.Reader`, and the API stays faked by `omnitest`.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI). Two new packages: `internal/module/memory` (FR-001) and
  `internal/hostread` (NFR-006, ADR-0009). No new dependency. `memory` imports only
  `manifest`, `module` and `hostread`; `cpu` gains `hostread` and loses gopsutil.

## Technical context
- Go 1.26, `CGO_ENABLED=0`, SDK `v1.0.14`, gopsutil `v4.26.8`, all unchanged.
- Reading used: `mem.VirtualMemoryWithContext`, which is stateless on every platform
  (spike T001, recorded in `docs/reference/omnismith-api-notes.md`). Only `Total` and
  `Available` are used. `Free`, `Used` and `UsedPercent` are **never** read (FR-005,
  FR-007).
- API operation added (read-only, sandbox acceptance only):
  | Operation | SDK call | Used for |
  |-----------|----------|----------|
  | `getEntity` | `EntityAPI.GetEntity(ctx, id).Fields(slugs).Execute()` | NFR-005: read `mem_total_mib` back |
- Existing seams reused unchanged: `manifest.Attribute.Platforms` and
  `manifest.Collectable` (ADR-0007), `collect` gating and startup log (004 FR-019…FR-023),
  `config` intervals and module switches (no new config keys).

## Approach

### 1. `internal/hostread` (NFR-006, ADR-0009)
- `doc.go`: the package contract, which is ADR-0008's two rules plus "only importer of
  gopsutil".
- `cpu.go`: `internal/module/cpu/reader_gopsutil.go` moved as is. `type CPU struct{}`
  with `Times`, `LoadAvg`, `Counts`, `Model`, the same method set as `cpu.Reader`, so no
  adapter is needed. Types `CPUTimes` (keeps the Guest/GuestNice comment) and `LoadAvg`.
- `memory.go`: `type Memory struct{}` with
  `Read(ctx) (MemoryReading, error)`, where `MemoryReading{Total, Available uint64}` is in bytes.
  Errors: wrapped read error; a nil result. A zero total is returned as-is: judging it
  is the module's job (FR-008). The doc comment says `Available` is gopsutil's emulation
  on macOS and on Linux < 3.14, and that callers gate it by platform.
- `hostread_test.go`: a real-host smoke test on whatever platform runs `go test`. It
  checks `Total > 0`, `Available ≤ Total`, and that the goroutine count is unchanged
  after reads (ADR-0005/0008). It asserts no fixed values.

### 2. `cpu` onto `hostread` (NFR-006: no behaviour change)
- `reader.go`: `type Times = hostread.CPUTimes`, `type Load = hostread.LoadAvg`; the
  `Reader` interface is unchanged.
- `Times.IdleTime()` cannot stay a method on an alias of a foreign type, so it becomes
  the exported function `cpu.IdleTime(t)`: which states count as idle is a spec 004
  decision and stays in `cpu`. *(As built: `Total()` stays a method on
  `hostread.CPUTimes`, since the counters being disjoint is a property of the reading.
  The plan had moved both sums into `cpu`.)*
- `New()`: `Reader: hostread.CPU{}`. Delete `reader_gopsutil.go`.
- The local `omissions` type is replaced by `module.Omissions`. The log record keeps its
  exact shape (`msg`, `module`, `keys`, `reasons`), and so does the error text.

### 3. `module.Omissions` (004 FR-016, 005 FR-013)
`internal/module/omissions.go`:
```go
type Omissions struct{ keys, reasons []string }
func (o *Omissions) Add(key string, err error)
func (o *Omissions) Len() int
func (o *Omissions) Log(log *slog.Logger, module string)   // one Error record, or nothing
func (o *Omissions) Err(module string) error               // "<module>: nothing could be read[: reasons]"
```

### 4. `internal/module/memory` (FR-001…FR-015)
- `Name = "memory"`, keys `used_pct`, `available`, `total`, `DefaultInterval = 30s`.
- Manifest: the three attributes. `used_pct` and `available` carry
  `Platforms: []string{"linux", "windows"}` (FR-011); `total` has none.
- Struct fields: `Reader Reader`, `GOOS string`, `Log *slog.Logger`. There is no clock
  and no sleep (FR-010).
- `Collect`:
  1. `r, err := Reader.Read(ctx)`: an error means `Omissions.Err`, so the provider fails (FR-012).
  2. `r.Total == 0`: the provider fails with a "total is zero" reason (FR-008).
  3. Observe `total = r.Total >> 20` (floor MiB, FR-006, FR-009).
  4. If `Collectable(availPlatforms, goos)`:
     - `r.Available > r.Total`: omit `available` and `used_pct`, one omission each (FR-008).
     - otherwise `available = r.Available >> 20` and
       `used_pct = clamp(float64(r.Total-r.Available)/float64(r.Total)*100)` (FR-005, FR-007).
  5. `Omissions.Log` once (FR-013); return the observations.
- Values are passed as `uint64`/`float64`. The core's validation accepts both for number
  and metric kinds; T005 confirms this.

### 5. Wiring and acceptance
- `cmd/omnistat/main.go`: `r.Register(memory.New()) // spec 005 FR-002`.
- `internal/omni/entities.go`: `EntityValues(ctx, id, slugs) (map[string]string, error)`,
  read-only, which handles both `attribute_values` shapes (map, or array of
  `{slug, value}`). Unit test with `omnitest`.
- `internal/cli/sandbox_test.go`: `TestSandbox_Memory`, modelled on `TestSandbox_CPU`.
  Config `modules.memory.interval: 1s`, `publish.interval: 2s`, 7s daemon. It asserts:
  - all three attributes exist, two metrics and one number;
  - the two series have ≥ 2 distinct timestamps, with `mem_used_pct` in [0,100] and
    `mem_available_mib` in (0, total];
  - `mem_total_mib` read back equals the test's own `/proc/meminfo` `MemTotal` kB ÷ 1024.

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/hostread` | The only gopsutil importer; stateless per-area readers | NFR-006, ADR-0009 |
| `internal/module/memory` | `memory` manifest + provider | FR-001…FR-015 |
| `internal/module` (existing) | `+ Omissions` | 004 FR-016, 005 FR-013 |
| `internal/module/cpu` (existing) | Reader moved out; aliases; uses `module.Omissions` | NFR-006 |
| `internal/omni` (existing) | `+ EntityValues` (read) | NFR-005 |

## Data flow
Scheduler tick (30s) → `memory.Collect` → one `hostread.Memory.Read` → observations
`{total, available, used_pct}` (keys) → core stamps, validates and buffers → publisher:
`mem_total_mib` as a dimension update, the two metrics ingested with their timestamps.
On macOS the core expects only `total` (004 FR-019).

## Configuration
No new settings. `modules.memory.enabled` / `modules.memory.interval` /
`modules.memory.attributes.<key>.*` work generically (001, 003, 004).

## Testing strategy
| Requirement | Test | Where |
|-------------|------|-------|
| FR-001, FR-003 | `TestManifest_DefaultSlugs` (keys, slugs, kinds, platforms), validates via `manifest.Validate` | `module/memory/memory_test.go` |
| FR-002 | registry default-on; disable via switches | `cmd` registry test / `module/memory` |
| FR-004 | `TestDefaultInterval` = 30s | `memory_test.go` |
| FR-005, FR-006 | floor MiB (`TestCollect_FloorsToWholeMiB`) | `memory_test.go` |
| FR-007 | `TestCollect_UsedPctFromBytesNotMiB`, clamp | `memory_test.go` |
| FR-008 | `TestCollect_ZeroTotal_Fails`, `TestCollect_AvailableExceedsTotal_OmitsPair` | `memory_test.go` |
| FR-009, FR-010 | two collects: both carry `total`; reader called once per collect, no sleep field | `memory_test.go` |
| FR-011 | `TestCollect_Darwin_TotalOnly` (no omission log), `TestCollect_Windows_AllThree`; core gating already tested (004) | `memory_test.go` |
| FR-012, FR-013 | `TestCollect_ReadError_Fails`, `TestCollect_OneOmissionRecord` | `memory_test.go` |
| 004 FR-016 / 005 FR-013 | `TestOmissions_*` | `module/omissions_test.go` |
| FR-014 | startup schedule log lists `memory` + macOS skips: existing generic tests; dry-run check T011 | `collect`, manual |
| NFR-004 | fakes only; `make crosscheck` | — |
| NFR-005 | `TestSandbox_Memory`; `EntityValues` unit test | `cli/sandbox_test.go`, `omni/*_test.go` |
| NFR-006 | all existing `cpu` tests pass; `hostread` smoke test; `grep gopsutil` only in `hostread` | `module/cpu`, `hostread` |

## Risks & unknowns
- The local sandbox API was down during the spike. If it is still down at T012,
  acceptance cannot run and the sync must say so.
- The number-dimension read-back shape (`attribute_values` map vs. array) is unverified
  against the real API; `EntityValues` handles both.
- The kernel < 3.14 emulation is accepted and undetectable (spec edge cases), so it stays
  untested.

## Decisions taken here that deserve an ADR
- ADR-0009: host readings live in one core package.
