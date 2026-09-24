---
feature: 005-memory-module
status: done             # draft | in-progress | done
plan: ./plan.md
---

# Tasks: `memory` module — how close a host is to swapping

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

Every task ends green on `make all` unless it says otherwise.

## Phase 0 — De-risking

- [x] **T001** *(throwaway spike, constitution I)* Verify gopsutil's memory reading
  against `/proc/meminfo`, check it for goroutines, cross-compile it, and read the
  platform sources for statefulness and emulation.
  (files: `docs/reference/omnismith-api-notes.md` only)
  — verify: notes updated; spike deleted. **Done 2026-09-24.** The sandbox half
  (number-dimension read-back) was not run because the local API was down; it moves to T012.

- [x] **T002** ADR-0009 (`proposed`) and its row in `specs/decisions/README.md` (NFR-006).
  (files: `specs/decisions/0009-*.md`, `specs/decisions/README.md`) — verify: `make specs-check`.

## Phase 1 — Core extraction (NFR-006, ADR-0009)

- [x] **T003** Tests first: `module.Omissions`, covering one record per collection,
  nothing logged when empty, and the error text with and without reasons (004 FR-016,
  005 FR-013).
  (files: `internal/module/omissions_test.go`) — verify: fails to compile (no type yet).

- [x] **T004** Implement `module.Omissions`; move `cpu` onto it, keeping the record's
  shape and error text unchanged.
  (files: `internal/module/omissions.go`, `internal/module/cpu/cpu.go`)
  — verify: T003 and every existing `cpu` test green.

- [x] **T005** `internal/hostread`: `doc.go`; move `cpu`'s gopsutil reader into
  `cpu.go` as `hostread.CPU` with the `CPUTimes`/`LoadAvg` types; add `memory.go`
  (`hostread.Memory`, `MemoryReading`); add the smoke test. In `cpu`: aliases,
  `IdleTime` as a function (`Total` stays on the reading type; see spec notes), `New()` on `hostread.CPU{}`, delete
  `reader_gopsutil.go`.
  (files: `internal/hostread/*`, `internal/module/cpu/{reader,usage,cpu}.go`, cpu tests
  (mechanical edits only)) — verify: `make all`; `grep -rn gopsutil internal cmd`
  lists only `internal/hostread`; `make crosscheck`.

## Phase 2 — `memory` module, tests first

- [x] **T006** Tests: manifest (FR-001, FR-003, FR-004, FR-011) and collect
  (FR-005…FR-013): floor MiB, pct from bytes, clamp, zero total, available > total,
  read error, darwin total-only, windows all three, one omission record, total on every
  collect, one reading per collect.
  (files: `internal/module/memory/{memory_test,fake_test}.go`)
  — verify: fails to compile (no package yet).

- [x] **T007** Implement `internal/module/memory` (`memory.go`, `reader.go`, `errors.go`).
  — verify: T006 green; `make all`.

## Phase 3 — Wiring and acceptance

- [x] **T008** Register `memory.New()` in `cmd/omnistat/main.go` (FR-002); check
  `omnistat schema plan`, `schema plan` with `memory` disabled, and `run --dry-run`
  (FR-014, US-5) against the fake-free dry paths.
  (files: `cmd/omnistat/main.go`) — verify: dry-run shows `mem_*` values matching `free -m`.

- [x] **T009** `omni.Client.EntityValues` (read-only `getEntity`) with an `omnitest`
  unit test covering both `attribute_values` shapes (NFR-005).
  (files: `internal/omni/entities.go`, `internal/omni/*_test.go`, `internal/omni/omnitest/*`)
  — verify: `make all`.

- [x] **T010** `TestSandbox_Memory` (NFR-005, US-1/1, US-2/1, US-3/1).
  (files: `internal/cli/sandbox_test.go`) — verify: `make sandbox` against the local API.

## Phase 4 — Sync

- [x] **T011** Gates: `make all`, `make test-race`, `make crosscheck`, `make specs-check`.
- [x] **T012** Sandbox acceptance run (`make sandbox`), recording what happened, including
  whether the API was reachable. Record anything learned in the API notes.
- [x] **T013** Sync: `spec.md` → `implemented` + implementation notes; ADR-0009 →
  `accepted`; `README.md` (module table + config example), `CHANGELOG.md` *Unreleased*,
  `internal/README.md` rows; this file and `plan.md` → done.

## Outcome (2026-09-24)

- T003–T011: `make all`, `make test-race`, `make crosscheck` and `make specs-check` are
  green.
- T012: `TestSandbox_Memory` passed on every run. `TestSandbox_Resolve` (002, not touched
  here) fails intermittently because of the platform's 100–280 ms search lag after a create
  (see spec implementation notes and API notes); it is flagged as a follow-up.
