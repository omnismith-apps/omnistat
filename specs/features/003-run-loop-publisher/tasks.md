---
feature: 003-run-loop-publisher
status: done              # draft | in-progress | done
plan: ./plan.md
---

# Tasks: Run loop, publisher & `hostname` module

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tests-first tasks precede their implementation; tick a task only when `make all` is green.

## Phase 1 — Contract & reference module (pure)
- [x] T001 `module.Provider` + `module.Observation` (plan §1); test that `machineid.Module` does **not** implement `Provider` (FR-005) and that `Static` fixtures can carry a provider for tests (files: internal/module/module.go, module_test.go, moduletest/fixtures.go) — verify: `make all`
- [x] T002 [P] Tests: `hostname` manifest (FR-023), trim / empty → error (FR-024), `DefaultInterval` = 5m (FR-025), injectable hostname func (files: internal/module/hostname/hostname_test.go) — verify: fails for the right reason
- [x] T003 Implement `internal/module/hostname` (files: internal/module/hostname/hostname.go) — verify: T002 passes

## Phase 2 — Schema & config groundwork
- [x] T004 [P] `schema.CurrentAttribute.OptionIDs` and `schema.Resolved.ListItems` (FR-012a); `omni.ReadSchema` fills option ids; `omnitest` discovery already emits `options[].id` — extend the discovery test to assert ids round-trip (files: internal/schema/schema.go, apply.go, apply_test.go, internal/omni/client.go, client_test.go) — verify: existing tests + new assertion green
- [x] T005 [P] Config: `publish.interval` (1s…1h, default 60s, FR-013) and `modules.<name>.interval` (1s…24h, FR-003); table tests for bounds, parse errors and testdata (files: internal/config/config.go, config_test.go, testdata/full.yaml, testdata/invalid.yaml) — verify: tests pass; the "module without provider" check is wired in T014 (config cannot see the registry)

## Phase 3 — Collection (pure, fake clock)
- [x] T006 [P] Tests: `Validate` per kind, ints/floats → float64, bad type, undeclared key, list option not declared → dropped with module/key in log, rest kept (FR-002) (files: internal/collect/validate_test.go)
- [x] T007 [P] Tests: `Buffer` — dims latest-wins (FR-007), metrics ordered, bound 5 000 drops oldest + counters (FR-008), `Snapshot`/`Ack` partial (dims acked by timestamp, metrics by cut point; a newer sample arriving between snapshot and ack survives) (FR-009) (files: internal/collect/buffer_test.go)
- [x] T008 [P] Tests: `Scheduler` under a fake clock — call at t=0 and every interval; deadline = interval; a slow provider is cancelled and counted failed, next call still happens (FR-004/010); a failing/panicking module does not affect others (FR-010); same module never overlaps, different modules run concurrently (FR-011, `-race`); `At` = clock at return (FR-006); `FirstRound` closes after every source's first attempt (FR-014); no goroutine leak after ctx cancel (NFR-002); `Once` returns failed module names (FR-018) (files: internal/collect/scheduler_test.go, clock_test.go)
- [x] T009 Implement `internal/collect`: `Clock`, `Value`/`Validate`, `Sample`, `Buffer`, `Source`, `Plan`, `Scheduler`, `Once` (files: internal/collect/{clock,validate,buffer,scheduler,once}.go) — verify: T006–T008 pass under `make test-race`

## Phase 4 — Publishing (fake API)
- [x] T010 [P] `omnitest`: routes `PATCH /entities/{id}` (store values, accept scalar and backfill objects, 404 unknown id) and `POST /entities/{id}/metrics` (append observations with `updated_at`, 202, 404 unknown id); `EntityValues(id)`, `EntityMetrics(id)` snapshots; `FailNext` works for both (files: internal/omni/omnitest/entities.go, server.go) — verify: `omnitest` self-tests
- [x] T011 [P] Tests: `publish.API` on `omni.Client` — payload shapes (string values, `updated_at` with `Z`, `attribute_slug` + string metric values), 404 → `omni.ErrNotFound`, 422 → `omni.ErrValidation` with `Fields` (FR-012/015) (files: internal/omni/publish_test.go)
- [x] T012 Implement `omni.UpdateEntity` / `omni.IngestMetrics`; `var _ publish.API = (*Client)(nil)` (files: internal/omni/publish.go) — verify: T011 passes
- [x] T013 [P] Tests: `Publisher.Publish` — one PATCH + `ceil(n/1000)` ingests (FR-012, NFR-003); empty batch → zero requests; list value → item id, unknown option dropped + logged (FR-012a); 404 → `ErrEntityGone`; 422 on dims → named slugs dropped and retried once; 422 on a chunk → chunk dropped; 503 (after transport retries) → error with partial `Ack` so the buffer keeps the rest (FR-015, FR-009); unchanged dims still sent (FR-016); 202 acked (FR-017); `Printer` text and JSON (`version: 1`), never fails (FR-021) (files: internal/publish/publish_test.go, printer_test.go)
- [x] T014 Implement `internal/publish`: `API`, `Backfill`, `Metric`, `Publisher`, `Ack`, `ErrEntityGone`, `Printer` (files: internal/publish/{api,publish,printer}.go) — verify: T013 passes

## Phase 5 — Run command & wiring
- [x] T015 [P] Tests (CLI + omnitest): one-shot order reconcile → resolve → collect → publish (US-1/1, US-1/2); partial provider failure → publishes the rest, exit 2 (US-1/3, FR-018); `schema.mode: verify|off` with missing schema fails before any collection (FR-022); `--dry-run` makes zero write requests, text and `--json` output, `--json` without `--dry-run` rejected (FR-021); config interval on a module without a provider → config error (T005 note); schedule logged at startup, values only at debug (FR-026/027) (files: internal/cli/run_test.go, helpers_test.go)
- [x] T016 [P] Tests (daemon, fake clock, `runDaemon(deps)` extracted): reconcile/resolve exactly once (FR-019); first publish right after the first round, then every interval (FR-014, US-2/1); observations carry collection time (US-2/2); empty buffer → no request (US-2/3); slow publish coalesces the next tick (FR-014); 503 for several ticks → buffer kept, then backfilled (US-3/1); bound reached → drops logged (US-3/2); 404 → exit non-zero naming the entity (US-3/3); ctx cancel → final publish within `http.timeout`, exit 0 (FR-020, NFR-005); daemon + dry-run prints per tick, writes nothing (US-4/2) (files: internal/cli/run_daemon_test.go)
- [x] T017 Implement `omnistat run [--daemon] [--dry-run] [--json]`: `identityTarget(p)` factored out of `identity.go`, reconcile per mode, `ExitPartial = 2`, sources from registry + config, `runOnce`, `runDaemon`, usage text (files: internal/cli/run.go, cli.go, identity.go) — verify: T015–T016 pass
- [x] T018 `cmd/omnistat`: register `hostname` (enabled by default); `stop()` after the first signal so a second one terminates (FR-020, FR-023); `internal/README.md` package map (files: cmd/omnistat/main.go, internal/README.md) — verify: `make build`, manual `run --dry-run`

## Phase 6 — Acceptance & sync
- [x] T019 Sandbox acceptance on the local API (project "Omnistat Test", `.env`): `schema apply` creates `hostname`; `run --dry-run` prints the hostname with a timestamp and makes no write; `run` publishes it (confirm via `identity` + entity search / UI history); `run --daemon` with `publish.interval: 5s`, `modules.hostname.interval: 2s`: first publish immediately, then every 5s, SIGTERM → final publish → exit 0; `TestSandbox_Run` build-tagged (`make sandbox`). Record findings in spec notes (files: internal/cli/sandbox_test.go, specs/…/spec.md)
- [x] T020 Sync: spec `status: implemented` + implementation notes (incl. `mode: off` ≡ `verify` in `run`, numbers-as-strings); plan `status: done`; `docs/reference/omnismith-api-notes.md` (value encoding, chunk size); README quick start + config example (`publish`, `modules.hostname.interval`, `run`); CHANGELOG (files: specs/…, docs/reference/omnismith-api-notes.md, README.md, CHANGELOG.md)
