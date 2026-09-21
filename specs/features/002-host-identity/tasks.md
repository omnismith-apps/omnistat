---
feature: 002-host-identity
status: done              # draft | in-progress | done
plan: ./plan.md
---

# Tasks: `machine-id` module & host identity

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.

## Phase 1 — Module (pure)
- [x] T001 [P] Tests: manifest (FR-001), Linux file precedence/trim/all-zero/missing (FR-004), macOS `ioreg` parsing (FR-004), static short-circuit/validation (FR-005/006/008), derivation vector & stability (FR-007), unsupported GOOS (files: internal/module/machineid/*_test.go)
- [x] T002 Implement `machineid.Module` (manifest, `Discover`, `Derive`, injectable fs/runner/GOOS) (files: internal/module/machineid/machineid.go, discover.go) — verify: T001 passes
- [x] T003 Config: `identity.static` + `OMNISTAT_IDENTITY` (env wins), ≤128, trim (FR-008/009) (files: internal/config/config.go, config_test.go, testdata)

## Phase 2 — API surface
- [x] T004 `identity.API` (`FindEntities`, `CreateEntity`) implemented by `omni.Client`; `omnitest` entity store already speaks search/create — add `RaceOnCreateEntity`-style hook usage in tests; error mapping (files: internal/identity/api.go, internal/omni/entities.go, entities_test.go)

## Phase 3 — Resolution
- [x] T005 [P] Tests: 0/1/>1 matches, oldest wins + duplicates reported (FR-010…013), create payload = identity only (FR-014), concurrent first run converges (US-1/3, `-race`), dry-run writes nothing (files: internal/identity/resolve_test.go, resolve_race_test.go)
- [x] T006 Implement `identity.Resolve`, `Host`, `Target` (files: internal/identity/resolve.go) — verify: T005 passes

## Phase 4 — Wiring
- [x] T007 Register `machine-id` as required in `cmd/omnistat`; `manifest.Desired.Find(module, key)`; CLI `identity [--json]` (FR-017) with dry-run resolution after reconciliation in configured mode (FR-016) (files: cmd/omnistat/main.go, internal/manifest/resolve.go, internal/cli/identity.go)
- [x] T008 CLI tests: output/exit codes, `--json`, no writes, raw id never logged (FR-018), required module cannot be disabled (FR-002) (files: internal/cli/identity_test.go)

## Phase 5 — Acceptance & sync
- [x] T009 Sandbox acceptance on the local API: `schema apply` (creates `machine_id`), `identity` read-only; the write path via the build-tagged `TestSandbox_Resolve` (`make sandbox`): create → reuse → exact match; duplicate via API → warning. Recorded in spec notes.
- [x] T010 Sync: spec `status: implemented` + notes; ADR-0004; README; CHANGELOG (files: specs/…, README.md, CHANGELOG.md)
