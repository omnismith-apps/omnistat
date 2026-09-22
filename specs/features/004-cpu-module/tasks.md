---
feature: 004-cpu-module
status: done             # draft | in-progress | done
plan: ./plan.md
---

# Tasks: `cpu` module — the first metric provider

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

Every task ends green on `make all` unless it says otherwise.

## Phase 0 — Setup and de-risking

- [x] **T001** Add the dependency and the portability gate (ADR-0008, NFR-005).
  Add `github.com/shirou/gopsutil/v4 v4.26.8` to `go.mod`; add a `make crosscheck`
  target building `CGO_ENABLED=0` for `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`,
  `windows/{amd64,arm64}`; call it from CI.
  (files: `go.mod`, `go.sum`, `Makefile`, `.github/workflows/ci.yml`)
  — verify: `make crosscheck` green for all six pairs; `make all` green.
  **Done 2026-09-22.** Deviation: the gopsutil requirement itself is added in T028,
  where the first import makes `go mod tidy` keep it; T001 delivered the gate.

- [x] **T002** Rename the `cpu` test fixture to free the name and its slugs (plan §4).
  `moduletest.CPU()` → `moduletest.Probe()`, module `probe`, slugs `probe_model`,
  `probe_usage_pct`, `probe_arch`; update every call site.
  (files: `internal/module/moduletest/fixtures.go`, `internal/module/module_test.go`,
  `internal/collect/scheduler_test.go`, `internal/cli/{identity,run,cli}_test.go`)
  — verify: `make all` green; `grep -rn "moduletest.CPU" .` returns nothing.

- [x] **T003** *(throwaway spike — do not merge the code, constitution I)* Confirm a
  **metric** attribute can be created and ingested against the real API: hand-register a
  one-metric module, `schema apply`, ingest two observations, read one back. Record what
  the platform actually accepted — `attribute_type`/`data_type` values, the ingest
  payload, the 422 shape if any, and `GetEntityChart`'s `Start`/`End` units — in the
  reference notes.
  (files: `docs/reference/omnismith-api-notes.md` only)
  — verify: notes updated with observed facts; spike branch discarded. Blocks T040's
  design, not its existence.
  **Done 2026-09-22.** Metric attributes need no special handling (`attribute_type: 1`
  reads back as `"type": "metric"`). `GetEntityChart` takes epoch **seconds** — passing
  milliseconds returns `200` with an empty series, not an error — and buckets at
  **`1 hour` by default**, so T040 must pass an explicit `bucket_width`. Findings in
  `docs/reference/omnismith-api-notes.md`; spike file deleted. The sandbox project now
  holds a `spike_usage_pct` attribute and a `spike-metric` entity, which cannot be
  removed (constitution IV).

## Phase 1 — Platform support in the core (tests first)

- [x] **T010 [P]** Failing tests: `Platforms` survives `Resolve` untouched by overrides,
  and `Desired` is byte-identical for `linux`/`darwin`/`windows` (FR-017, FR-020).
  (files: `internal/manifest/resolve_test.go`)
  — verify: tests fail to compile/assert for the right reason (field absent).

- [x] **T011 [P]** Failing tests: a manifest declaring a platform outside
  `KnownPlatforms` is a fatal validation error naming module, key and the known set
  (FR-017).
  (files: `internal/manifest/validate_test.go`)
  — verify: test fails for the right reason.

- [x] **T012** Implement `Platforms` on `manifest.Attribute` and
  `manifest.DesiredAttribute`, `manifest.KnownPlatforms`, and the validation rule.
  (files: `internal/manifest/manifest.go`, `internal/manifest/resolve.go`,
  `internal/manifest/validate.go`)
  — verify: T010 and T011 pass; every existing manifest test still passes (empty
  `Platforms` must mean "everywhere").

- [x] **T013** Failing tests: `Sources(desired, mods, intervals, goos)` drops
  uncollectable attributes into `Unsupported`, returns one `Skipped` per dropped key,
  skips a module whose attributes are all uncollectable, and returns the same `Desired`
  regardless of `goos` (FR-018, FR-019, FR-022).
  (files: `internal/collect/scheduler_test.go`)
  — verify: tests fail for the right reason.

- [x] **T014** Implement the `Sources` signature change, `Source.Unsupported`, the
  `Skipped` type, and the `collectOne` rule that an observation for an uncollectable key
  is dropped at **debug**, never as an error (FR-016, FR-019).
  (files: `internal/collect/scheduler.go`, `internal/cli/run.go` for the call site)
  — verify: T013 passes; `make all` green.
  **Done 2026-09-22.** `App.GOOS` was added alongside (empty = `runtime.GOOS`) so the
  CLI-level gating of T031 is testable off-platform; the ten existing `Sources` call
  sites in tests took the new argument mechanically.

## Phase 2 — The `cpu` module (tests first)

- [x] **T020 [P]** Failing tests: manifest declares exactly the seven attributes of
  FR-001 with the right keys, slugs, kinds, options and order; `load1/5/15` declare
  `{linux, darwin}` and the rest declare none; `DefaultInterval` is 10s; the module is
  enabled by default and disablable (FR-001, FR-002, FR-004, FR-018).
  (files: `internal/module/cpu/cpu_test.go`)
  — verify: test fails for the right reason (package absent).

- [x] **T021** Create `internal/module/cpu`: the `Reader` interface, `Times`/`Load`
  types, the manifest, `Name`, `DefaultInterval`, and a `fakeReader` for tests. No
  `Collect` body yet beyond a stub.
  (files: `internal/module/cpu/cpu.go`, `internal/module/cpu/reader.go`,
  `internal/module/cpu/fake_test.go`)
  — verify: T020 passes.

- [x] **T022 [P]** Failing tests for the usage arithmetic (FR-005, FR-013): idle-only ⇒
  0; all-busy ⇒ 100; an 8-core host saturated ⇒ 100 not 800; guest time inside
  user/nice is not double-counted; `Δtotal == 0` ⇒ no observation; a counter that went
  backwards ⇒ no observation; the result is clamped and never NaN or ±Inf.
  (files: `internal/module/cpu/usage_test.go`)
  — verify: tests fail for the right reason.

- [x] **T023** Implement `pct(prev, cur Times) (float64, bool)` per plan §3.
  (files: `internal/module/cpu/usage.go`)
  — verify: T022 passes.

- [x] **T024 [P]** Failing tests for the rate state machine (FR-011, FR-012, FR-014):
  first call sleeps exactly once for `Prime` and yields usage; second call yields usage
  **without** sleeping; a deadline shorter than `Prime` yields no usage but keeps the
  dimensions; cancellation mid-prime leaves the baseline consistent; a `Reader.Times`
  error leaves the previous baseline untouched.
  (files: `internal/module/cpu/usage_test.go`)
  — verify: tests fail for the right reason. No test may call `time.Sleep`.

- [x] **T025** Implement `Collect`'s usage branch: the mutex, the retained `last`, the
  prime path with the injected `Sleep`/`Now`, and the deadline check.
  (files: `internal/module/cpu/cpu.go`, `internal/module/cpu/usage.go`)
  — verify: T024 passes.

- [x] **T026 [P]** Failing tests for the remaining values and partial collection
  (FR-006…FR-010, FR-015, FR-016): load averages forwarded unchanged and absent when the
  platform does not declare them; model trimmed to its first line and omitted when empty;
  counts; `arch` emitted as `runtime.GOARCH`; each reader method failing in isolation
  costs only its own values; **all** failing ⇒ provider error; omissions logged once with
  keys and reasons.
  (files: `internal/module/cpu/cpu_test.go`)
  — verify: tests fail for the right reason.

- [x] **T027** Implement the dimension and load-average branches of `Collect` and the
  single omission log record.
  (files: `internal/module/cpu/cpu.go`)
  — verify: T026 passes.

- [x] **T028** Implement `gopsutilReader` — the only file importing gopsutil — using
  `cpu.Times(false)`, `load.Avg()`, `cpu.Counts(true)`, `cpu.Info()`. `cpu.Percent` is
  forbidden (ADR-0006/0008). Add a `-race` test calling `Collect` concurrently (FR-014).
  (files: `internal/module/cpu/reader_gopsutil.go`,
  `internal/module/cpu/usage_race_test.go`)
  — verify: `make test-race` green; `make crosscheck` still green; a one-off manual
  `Collect` on this Linux host returns a plausible percentage.

## Phase 3 — Wiring and observability

- [x] **T030** Register `cpu` enabled-by-default; log the schedule and every `Skipped`
  once at startup; print skips in dry-run text output and add `"skipped"` to the dry-run
  JSON document (FR-002, FR-023, US-5/3).
  (files: `cmd/omnistat/main.go`, `internal/cli/run.go`, `internal/publish/printer.go`)
  — verify: `./bin/omnistat run --dry-run` on this host shows cpu values and no skips;
  a test forcing `goos=windows` shows three skipped load attributes.

- [x] **T031** CLI tests (FR-021, FR-022, FR-024, NFR-003): disabled and unsupported are
  distinguishable in the log and the dry-run output; explicitly enabling a module that is
  uncollectable here skips rather than errors; values appear only at debug level and no
  record carries a raw reading; with `cpu` enabled a publish still makes exactly
  `1 + ceil(n/1000)` requests.
  (files: `internal/cli/run_test.go`)
  — verify: tests pass against `omnitest`; no real network in `go test`.
  **Done 2026-09-22.** Writing the disabled-vs-unsupported test uncovered a
  pre-existing bug: *any* `modules.<name>.enabled: false` failed with `config:
  override for unknown module "<name>"`, because a block carrying only `enabled`
  or `interval` was still recorded as a schema override and then resolved against
  the manifests the switch had just removed. Spec 001 FR-006 and US-4/1 require
  disabling to work, so it is fixed here with a regression test in
  `internal/config/config_test.go` (switches are not schema overrides).

## Phase 4 — Acceptance and sync

- [x] **T040** Sandbox acceptance for the metric path (NFR-006), using T003's findings.
  Add `omni.EntityChart` (read-only, `GetEntityChart`) and `TestSandbox_CPU` under the
  `sandbox` build tag: `schema apply`, `run --daemon` for ~3 publish intervals against the
  real project, then read `cpu_usage_pct` back and assert several points with distinct
  timestamps.
  (files: `internal/omni/entities.go`, `internal/cli/sandbox_test.go`, `Makefile` if the
  `sandbox` target needs a new case)
  — verify: `make sandbox` green against the sandbox project; record every deviation in
  `spec.md`'s implementation notes.

- [x] **T041** Update `spec.md`: `status: implemented`, `implemented:` date, an
  implementation-notes section recording deviations and sandbox findings. Move ADR-0006,
  ADR-0007 and ADR-0008 from `proposed` to `accepted` (human sign-off) and update the ADR
  index.
  (files: `specs/features/004-cpu-module/spec.md`, `specs/decisions/000{6,7,8}-*.md`,
  `specs/decisions/README.md`)
  — verify: `make specs-check` green; no `[NEEDS CLARIFICATION]` remains.

- [x] **T042** Update the human-facing docs: `README.md` (module list, the `cpu` config
  example, the note that load averages are absent on platforms without them),
  `internal/README.md` (new package row), `CHANGELOG.md` under *Unreleased*, and
  `docs/reference/omnismith-api-notes.md` (metric attribute creation, chart read-back).
  (files: as listed)
  — verify: `make all` green; README's config example matches the real keys.

## Notes

- T003 is the one task whose output is documentation rather than code. Doing it first
  means T040 is a write-up of a known-good path instead of a discovery exercise.
- Phase 1 is independent of Phase 2 and can land on its own; it is useful to `ip-address`
  and every later module even if `cpu` slips.
- Acceptance runs on Linux only. macOS and Windows are covered by T001's cross-compile
  gate and by the faked `Reader` in unit tests — that is the whole of the evidence for
  those platforms, and `spec.md` NFR-006 says so.
