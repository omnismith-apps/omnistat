---
feature: 007-linux-service
status: done             # draft | in-progress | done
plan: ./plan.md
---

# Tasks: omnistat as a systemd service on Linux

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

The local gate is `make all crosscheck` plus `GOOS=windows go vet ./...` and the Windows
lint pass. Windows tests run in CI only, when the owner pushes. A Windows failure there
reopens the task it belongs to (006's batching rule). No task runs anything as root on
the dev host: real systemd work happens only in the disposable containers of T050.

## Phase 0 — Shared service core (refactor, no behaviour change)
- [x] T001 Create `internal/service`. Move these out of `winsvc`: `Plan`/`Step`/
  `Describe`/`Apply`, `Checked`, `InstallOptions`, the settings merge
  (`ResolveSettings`), `watch` (as `Watch` taking a check function), `ErrNotInteractive`
  and `ErrNoToken`. Add `Prompter`. Update `winsvc`, its fakehost, `host_windows.go` and
  `cli/service.go`. Existing tests move with the code or keep passing unchanged. (files:
  `internal/service/*`, `internal/winsvc/*`, `internal/cli/service.go`) — verify:
  `make all crosscheck`, `GOOS=windows go vet ./...`, Windows lint.

## Phase 1 — Shared behaviour changes (tests first)
- [x] T010 Tests first for FR-017 (project-id prompt: env, then stored, then config file,
  then prompt; no terminal leaves it to the pre-check) and for FR-013's name list (Linux
  lower-case proxy variables). (files: `internal/service/settings_test.go`) — verify:
  fails for the missing prompt.
- [x] T011 Implement FR-017 in `ResolveSettings`. Add `PromptLine` to `winsvc.Host`, the
  Windows host and fakehost. (files: `internal/service/settings.go`, `internal/winsvc/*`)
  — verify: T010 passes; Windows vet/lint.
- [x] T012 [P] Tests first for FR-014: the shipped starter loads to the defaults, each
  block uncommented alone loads, every `yaml` key of the config file appears, and the
  token and restart notes are present. (files: `internal/config/starter_test.go`) —
  verify: fails, no starter yet.
- [x] T013 Write `internal/config/starter.yaml` and embed it as `config.Starter`. (files:
  `internal/config/starter.yaml`, `internal/config/starter.go`) — verify: T012 passes.
- [x] T014 Windows starter step (FR-014, amends 006): add `CreateFile` to `winsvc.Host`
  (Windows host with `O_EXCL`, fakehost), and a "create starter" step when the file is
  absent. Tests: absent means created once, present means untouched, and the dry-run
  shows it. (files: `internal/winsvc/*`) — verify: `make all`, Windows vet/lint.

## Phase 2 — systemd backend
- [x] T020 [P] Env file: tests first (quoting round-trip over `'`, `"`, `$`, `` ` ``, `%`,
  `\`, spaces, empty; a newline is rejected; hand-edited forms parse; the parse error
  names the line, not the content), then `FormatEnv`/`ParseEnv`. (files:
  `internal/systemd/envfile*.go`) — FR-013, spec edge case — verify: tests pass.
- [x] T021 [P] Unit text: golden test, then `UnitText()`. The marker comes first, and
  every directive of the plan is present. (files: `internal/systemd/unit*.go`,
  `testdata/omnistat.service`) — FR-006–FR-010, FR-023 — verify: golden matches;
  `systemd-analyze security --offline=true testdata/omnistat.service` ≤ 2.0 on the dev
  host (manual, recorded).
- [x] T022 [P] Journal and notify: tests first (priority per level for text and json;
  `PriorityWriter`; `JournalStream` matching and mismatching dev:ino; `Notify` to a
  `unixgram` listener, path and abstract; no-op without the socket), then
  `journal*.go`, `notify.go`. (files: `internal/systemd/journal*.go`, `notify*.go`) —
  FR-010, FR-022, FR-023 — verify: tests pass.
- [x] T023 Host seam: the `Host` interface, `File` and `Unit` types, `fakehost`, the real
  `host_linux.go` (files, termios prompts, `systemctl`, `ParseShow`) and
  `host_other.go`. Tests for the real host on `t.TempDir()`: `WriteFile` is atomic and
  sets the mode, `CreateFile` does not clobber, `ParseShow` handles `not-found`,
  `loaded` and `masked`, and `Secure` is tested only when running as root. (files:
  `internal/systemd/host*.go`, `internal/systemd/fakehost/*`) — NFR-004 — verify: tests
  pass; crosscheck.
- [x] T024 Install plan: tests first on the fakehost, then `PlanInstall`. Cover:
  - not root and not booted (FR-002, FR-004);
  - fresh install, with its step order and file modes;
  - an upgrade that keeps settings and the config (FR-018);
  - `--replace-token`;
  - foreign units: without the marker, from another fragment, masked (FR-019);
  - loose directory and config modes fixed (FR-012);
  - an existing config is not overwritten (FR-014);
  - a failed start, and the watch (FR-011);
  - a dry-run that makes zero host calls, shows the unit text and no secrets (FR-016,
    FR-024);
  - install run from the installed copy.

  (files: `internal/systemd/install*.go`) — verify: tests pass.
- [x] T025 Uninstall plan: tests first, then `PlanUninstall`. Cover: not installed, a
  foreign unit, full removal order, kept items with drop-ins (FR-020, FR-021), and the
  dry-run. (files: `internal/systemd/uninstall*.go`) — verify: tests pass.

## Phase 3 — CLI and runtime wiring
- [x] T030 CLI backend choice (FR-001, FR-003):
  - add `SystemdHost`, the `installer` adapters, the `unit:` line and `LogHint`;
  - make the usage text neutral;
  - macOS keeps its message.

  cli tests with the systemd fakehost and `omnitest`:
  - install, dry-run, pre-check equal to `identity`'s message, no secrets, not root;
  - uninstall;
  - the project prompt (FR-017);
  - `GOOS=darwin` is unsupported.

  (files: `internal/cli/service.go`, `internal/cli/cli.go`, `internal/cli/service_test.go`,
  `internal/cli/service_systemd_test.go`) — verify: `make all`.
- [x] T031 Readiness and stop notify in the daemon (FR-010, FR-023). Tests first in
  `run_daemon_test.go`:
  - a `unixgram` listener as `NOTIFY_SOCKET` receives `READY=1` after startup and not in
    dry-run;
  - on cancel it receives `STOPPING=1` and `EXTEND_TIMEOUT_USEC` equal to
    `http.timeout` + 5s;
  - a startup failure sends no `READY`.

  (files: `internal/cli/run.go`, `internal/cli/run_daemon_test.go`) — verify: tests pass.
- [x] T032 Journal wiring (FR-022): add `App.Journal` and the `newLogger` branch, and in
  `main.go` detect the journal and wrap stderr in `PriorityWriter`. cli test: with
  `Journal` set, records carry `<N>` and `fail()` lines carry `<3>`. (files:
  `internal/cli/cli.go`, `internal/cli/logging.go`, `cmd/omnistat/main.go`, a test) —
  verify: `make all`.

## Phase 4 — Acceptance, docs, sync
- [x] T040 README: a Linux service section parallel to the Windows one, covering install
  (prompt and `--preserve-env`), what is created, the operations table (configure,
  upgrade, rotate, proxy, start/stop, logs, remove), drop-ins, and migrating from a
  hand-written unit. Mention the starter config in the Windows section. Update the
  status line. (files: `README.md`) — NFR-007 — verify: read-through.
- [x] T041 `scripts/e2e-systemd.sh` (plan: container harness) and a `make e2e-systemd`
  target (manual, not part of `make all`). (files: `scripts/e2e-systemd.sh`,
  `scripts/e2e/*.Dockerfile` or inline, `Makefile`) — NFR-005 — verify: runs on Fedora 44.
- [x] T050 Container acceptance (NFR-005/1) on Fedora 44, Debian 12, Ubuntu 24.04,
  Rocky 9 and Rocky 8, against the local API. Record per-distro results, the
  `systemd-analyze security` score (NFR-002) and any deviation in the spec's
  implementation notes. — verify: all scenarios PASS, or a deviation recorded and agreed.
- [x] T051 `acceptance.md` runbook for the owner's Fedora host (NFR-005/2), and 006
  runbook additions for the starter and project prompt (NFR-006). (files:
  `specs/features/007-linux-service/acceptance.md`,
  `specs/features/006-windows/acceptance.md`) — verify: owner runs it.
- [x] T052 Sync:
  - draft ADR-0012 and accept it after owner review; *(accepted 2026-09-25, after the owner's runbooks passed)*
  - edit spec 006 (FR-012/FR-027 amended, US-7/3) and note 003's journal and notify
    extensions;
  - CHANGELOG *Unreleased*;
  - spec 007 `status: implemented` with implementation notes;
  - tick the tasks.

  (files: `specs/decisions/0012-*.md`, `specs/features/00{3,6,7}-*/spec.md`,
  `CHANGELOG.md`) — verify: `make specs-check all`.
