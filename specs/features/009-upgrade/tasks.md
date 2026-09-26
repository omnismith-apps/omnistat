---
feature: 009-upgrade
status: in-progress      # draft | in-progress | done
plan: ./plan.md
---

# Tasks: `omnistat upgrade`

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

The local gate is `make all crosscheck` (lint includes the Windows pass). Windows unit
tests run in CI only, when the owner pushes. No task runs anything as root on the dev
host: real systemd work happens only in the disposable container of T050.

## Phase 0 — Setup
- [x] T001 Add `golang.org/x/net` (for `http/httpproxy`, FR-012) to `go.mod`. (files:
  `go.mod`, `go.sum`) — verify: `make all`.

## Phase 1 — `internal/upgrade`, tests first
- [x] T010 [P] Tests for FR-005/FR-007: `ParseVersion` (with and without `v`, rc, `dev`,
  `git describe` with and without `-dirty`, garbage), `Compare` (SemVer pre-release
  precedence), `ParseVersionOutput`. (files: `internal/upgrade/version_test.go`) —
  verify: fails, no code yet.
- [x] T011 Implement `version.go`. — verify: T010 passes.
- [x] T012 [P] Tests for FR-006/FR-008/FR-015: `ReleasesURL` policy table, `AssetName`,
  `Resolve` against httptest (latest, pinned with and without `v`, pre-release, 404,
  missing asset, malformed or duplicate lines). (files:
  `internal/upgrade/release_test.go`) — verify: fails.
- [x] T013 Implement `release.go` and the `get` helper of `client.go`. — verify: T012
  passes.
- [x] T014 [P] Tests for FR-010/FR-012: `ProxySettings` (the environment wins even when
  it sets only `NO_PROXY`, stored fallback, lower-case), the transport proxy for a
  remote URL and a loopback bypass, and an HTTPS→HTTP redirect refused. (files:
  `internal/upgrade/client_test.go`) — verify: fails.
- [x] T015 Implement `client.go`. — verify: T014 passes.
- [x] T016 [P] Tests for FR-009/FR-011: `Fetch` with tar.gz and zip archives built in the
  test (ok, hash mismatch, no binary entry, a binary in a sub-directory ignored, size
  limit), and `Stage` (leftovers removed, 0700 on Unix, cleanup removes it). (files:
  `internal/upgrade/fetch_test.go`, `internal/upgrade/stage_test.go`) — verify: fails.
- [x] T017 Implement `fetch.go` and `stage.go`. — verify: T016 passes.
- [x] T018 Test and implement `runner.go` (FR-013): `ExecRunner.Version` and `Install`
  against a helper process (re-exec of the test binary): the exit status is propagated
  and the environment inherited. (files: `internal/upgrade/runner.go`,
  `internal/upgrade/runner_test.go`) — verify: passes on Linux; Windows vet.

## Phase 2 — Backends
- [x] T020 [P] `service.Installation`; `systemd.Installation` with tests (not root,
  not booted, not installed, foreign unit, installed with stored settings, broken
  settings file). (files: `internal/service/plan.go`, `internal/systemd/installation.go`,
  `internal/systemd/installation_test.go`) — verify: tests pass.
- [x] T021 [P] `winsvc.Installation` with tests (not elevated, not installed, foreign,
  installed). (files: `internal/winsvc/installation.go`,
  `internal/winsvc/installation_test.go`) — verify: tests pass; Windows vet/lint.
- [x] T022 Windows, upgrade run from the installed binary (FR-013a): `RunsFrom`,
  `StepAside` and `Aside.Finish` (restore, or delete at restart), tested with plain
  files; wired in the CLI before the hand-over, never in a dry-run. (files:
  `internal/upgrade/stepaside*.go`, `internal/cli/upgrade.go`) — verify: tests; Windows
  vet/lint; owner acceptance (upgrade from the installed copy).

## Phase 3 — CLI
- [x] T030 CLI tests first (FR-001–FR-004, FR-013, FR-014, FR-016, FR-017), on both
  fakehosts with a fake runner and an httptest release server:
  - usage and the flag conflict;
  - not root; not installed; foreign; macOS;
  - `--check` exits 0, 2 and 1;
  - up to date; installed newer; unknown installed version;
  - dry-run passes `--dry-run`; success summary; a failing install status propagated;
  - checksum mismatch and version mismatch never reach the install;
  - downgrade wording; the Windows zip; staging cleaned;
  - no secret printed.

  (files: `internal/cli/upgrade_test.go`) — verify: fails.
- [x] T031 Implement `internal/cli/upgrade.go`, `installer.Installation`, the `App`
  fields and usage. (files: `internal/cli/upgrade.go`, `internal/cli/service.go`,
  `internal/cli/cli.go`) — verify: T030 passes; `make all crosscheck`.

## Phase 4 — Acceptance & docs
- [x] T040 README: an upgrade section for both platforms (`upgrade`, `--check`,
  `--version`, proxy, mirror, manual fallback); the "Upgrade" rows of both tables
  (NFR-007). CHANGELOG *Unreleased*. (files: `README.md`, `CHANGELOG.md`) — verify:
  read-through.
- [x] T050 Container acceptance (NFR-006): `scripts/e2e-mirror` (a loopback static
  server) and `scripts/e2e-upgrade.sh`. The run installs the v0.3.0 GitHub release, then
  checks:
  - refusal without root;
  - `--check` exits 2;
  - the dry-run changes nothing;
  - the upgrade to a local `v0.99.0` build: settings, config and entity kept;
  - idempotence, and `--check` exits 0;
  - a tampered checksum is refused;
  - rollback with `--version v0.3.0`;
  - an HTTP non-loopback mirror is refused;
  - no leftovers.

  Make target `e2e-upgrade`. (files: `scripts/e2e-mirror/main.go`,
  `scripts/e2e-upgrade.sh`, `Makefile`) — verify: all checks pass on fedora44, debian12
  and rocky8.
- [ ] T051 Owner acceptance on Linux and Windows (NFR-006), with a published release
  candidate. — verify: owner confirms.
- [ ] T052 Sync: spec → `implemented` with notes and deviations, plan → `done`, ADR-0013
  accepted by the owner, memory. — verify: `make specs-check`.
