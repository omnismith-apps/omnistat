---
feature: 012-ups-module
status: in-progress      # draft | in-progress | done
plan: ./plan.md
---

# Tasks: `ups` module — UPS power state, battery and load from apcupsd

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

Every task ends green on `make all` unless it says otherwise. 011 T001–T009 come first.

## Phase 0 — De-risking

- [x] **T001** *(spike, read-only)* Read apcupsd 3.14.14's NIS server and status writer:
  framing, self-test codes, `STATUS` vs `STATFLAG`, units, dates, `XONBATT`, `BATTDATE`.
  *FR-002, FR-014.* *Verify:* plan "Technical context". Outcome: FR-002 is complete, and
  the flags must come from `STATFLAG`.

## Phase 1 — Core additions

- [x] **T002** *(tests first)* Module settings: `config` keeps scalar extra keys under
  `modules.<name>`; `module.Configurable`; `cli` validates and configures them before
  any network call; `module.Describer` adds fields to "module scheduled". *FR-005,
  FR-021.* *Verify:* `config` and `cli` tests.
- [x] **T003** *(tests first)* Failure logging: first occurrence at warn, repeats counted
  at debug, recovery at info with the count, a new reason logged again; counts in the
  daemon's publish summary. *FR-018 (amends 003 FR-010).* *Verify:* `collect` and
  `cli/run_daemon_test.go`.

## Phase 2 — The module

- [x] **T004** *(tests first)* `internal/module/ups/nis.go`: the NIS exchange with
  deadline, frame and size limits, error replies, and connection closing on every
  path, against an in-process fake server. *FR-009, FR-017, FR-019.* *Verify:*
  `nis_test.go`.
- [x] **T005** *(tests first)* `status.go`: typed field parsing (numbers with units,
  `STATFLAG`, datetimes, both `BATTDATE` forms, self-test codes), absent vs invalid.
  *FR-010, FR-012, FR-013.* *Verify:* `status_test.go` with recorded replies.
- [x] **T006** *(tests first)* `ups.go`: manifest (22 attributes, Linux only, list
  options), settings, key (FR-006/007), observations (FR-011…FR-015), omissions
  (FR-016), failures (FR-017), first-success info (FR-022), the module disabled by
  default and registered. *FR-001…FR-008, FR-010…FR-017, FR-020, FR-022.* *Verify:*
  `ups_test.go`, and `go run ./cmd/omnistat run --dry-run` against a fake NIS server and
  `omnitest`.

## Phase 3 — Docs and acceptance

- [x] **T007** README (module table, settings, apcupsd setup, clear-text note), starter
  config, `internal/README.md`, CHANGELOG, ADR-0015. *NFR-004.* *Verify:* `make
  specs-check`, and starter-config tests.
- [x] **T008** `cli/sandbox_ups_test.go`, plus the `scripts/e2e-nis` fake server and the
  e2e-systemd checks. *NFR-006.* *Verify:* `make sandbox`, `make e2e-systemd` —
  **both require the local API**.
- [ ] **T009** Sync: spec `status: implemented` with implementation notes, plan `done`,
  ADR-0015 accepted (owner). Owner acceptance on the UPS host is recorded when done.
  *Verify:* `make specs-check`.
