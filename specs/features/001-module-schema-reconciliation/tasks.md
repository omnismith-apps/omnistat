---
feature: 001-module-schema-reconciliation
status: done              # draft | in-progress | done
plan: ./plan.md
---

# Tasks: Module manifests & schema reconciliation

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

## Phase 0 — Setup
- [x] T001 Pin deps: `github.com/omnismith-sdk/go@v1.0.14`, `github.com/goccy/go-yaml`; create `internal/omni/doc.go` importing the SDK so `tidy` keeps it (files: go.mod, go.sum, internal/omni) — verify: `make tidy && make all`

## Phase 1 — Manifest (pure)
- [x] T010 [P] Tests for manifest types & `Validate` — every error class of FR-001…005, all-at-once reporting (files: internal/manifest/validate_test.go) — verify: fails to compile / fails
- [x] T011 Implement `Kind`, `Manifest`, `Attribute`, `Template`, `HostTemplate` const, `Validate` (files: internal/manifest/manifest.go, validate.go) — verify: T010 passes
- [x] T012 [P] Tests for `Overrides` + `Resolve`: precedence FR-007, name/description FR-008, collisions & unknown keys FR-009, union onto shared template FR-011, host template override FR-003 (files: internal/manifest/resolve_test.go)
- [x] T013 Implement `Overrides`, `Desired`, `Resolve` (files: internal/manifest/resolve.go) — verify: T012 passes

## Phase 2 — Schema diff (pure)
- [x] T020 [P] Tests for `Diff`: each action type, each conflict class (kind, data type), list option add / ignore extra / case-sensitive, bind preserves, name/description not conflicts, determinism FR-013…019 (files: internal/schema/diff_test.go)
- [x] T021 Implement `Current`, `Plan`, `Action`, `Conflict`, `Diff`, `Resolved` (files: internal/schema/schema.go, diff.go) — verify: T020 passes
- [x] T022 Text and JSON (`version: 1`) renderers for `Plan` FR-020 + tests (files: internal/schema/render.go, render_test.go)

## Phase 3 — Omnismith client
- [x] T030 Fake Omnismith API `omnitest.Server`: discovery, permissions, create template/attribute/list item, patch attribute; auth/project header checks; duplicate slug → 422; fault injection `FailNext`, `Before` hook (files: internal/omni/omnitest/server.go, store.go)
- [x] T031 [P] Retry/deadline/User-Agent transport + tests NFR-003 (files: internal/omni/transport.go, transport_test.go)
- [x] T032 `omni.Client` implementing `schema.API` over the SDK; error mapping (`ErrAlreadyExists`, `ErrForbidden`, `ErrUnauthorized`, `ErrNoProject`, `*ValidationError`, `*APIError`); `Current` builder; `MyPermissions` (files: internal/omni/client.go, errors.go, schema.go) + tests through the fake FR-012, FR-024, FR-027 (files: internal/omni/client_test.go)

## Phase 4 — Apply
- [x] T040 Tests for `Apply`: pre-flight refusal FR-022, order FR-019/023, race re-read FR-024 (object present → continue; absent → original error), partial failure report FR-026, resolved ids FR-025, verify-read after binds (files: internal/schema/apply_test.go)
- [x] T041 Implement `Apply`, `Result`, `ActionResult` (files: internal/schema/apply.go) — verify: T040 passes
- [x] T042 Concurrent applies converge — two goroutines on one fake, `-race` (files: internal/schema/apply_race_test.go)

## Phase 5 — Modules & config
- [x] T050 `module.Module` interface, `Registry`, `Enabled(settings)`, unknown-module error; fixture modules in `moduletest` (files: internal/module/module.go, moduletest/fixtures.go, module_test.go) FR-006, US-4
- [x] T051 `config.Load`: YAML + env precedence, defaults, validation (mode enum, unknown module, host template pattern), → `Settings`, `manifest.Overrides`; golden fixtures (files: internal/config/config.go, config_test.go, testdata/*.yaml) FR-006…010, IV

## Phase 6 — CLI & wiring
- [x] T060 `cli.Run`: global flags, `version`, `schema plan|apply|verify`, exit codes FR-021, slog setup FR-028 (files: internal/cli/*.go) + tests via fake server (files: internal/cli/schema_test.go, logging_test.go)
- [x] T061 Wire `cmd/omnistat/main.go` to `cli.Run`; `make all` green; `omnistat schema plan` end-to-end against the fake (files: cmd/omnistat/main.go, internal/cli/e2e_test.go)
- [x] T062 Manual acceptance against the "Omnistat Dev" sandbox: US-1/1–3 with a real token (dry-run first) — record outcome in spec "Implementation notes"
- [x] T063 Sync: spec `status: implemented` + deviations; ADR-0002/0003; README usage; CHANGELOG (files: specs/…, README.md, CHANGELOG.md)
