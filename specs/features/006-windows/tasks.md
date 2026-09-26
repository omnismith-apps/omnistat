---
feature: 006-windows
status: done             # draft | in-progress | done
plan: ./plan.md
---

# Tasks: omnistat on Windows — identity, service and logs

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

Windows-only code can only run in CI (no local Windows or Wine). Tasks that add such
tests end with "owner pushes, `go-windows` green". The owner manages git, so these CI
checks are batched: the tasks land locally, each green on `make all crosscheck`, and CI
runs when the owner pushes. A Windows failure then reopens the task it belongs to.

## Phase 0 — Setup
- [x] T001 Windows CI job and local gate (NFR-004). Add `go-windows` to
  `.github/workflows/ci.yml` (`windows-latest`, `go test ./...`). Add `vet-windows` to
  the Makefile and make `crosscheck` depend on it. Fix whatever the existing suite does
  wrong on Windows, as test fixes or small bug fixes, each noted in the spec's
  implementation notes. (files: `ci.yml`, `Makefile`, any failing `_test.go`) — verify:
  `make all crosscheck`; owner pushes, `go-windows` green. *(Local part done 2026-09-25, with `.gitattributes` (LF) and Windows lint in `make lint`/CI added. CI pending: the owner's push. T010+ went ahead in the same batch, per the batching rule above.)*

## Phase 1 — Identity
- [x] T010 Tests first for FR-002/FR-003 with a faked `MachineGUID`: a value, empty,
  all-zero (hyphenated), an error, the source name, and the derivation equal to the
  ADR-0004 vector for the same raw input. (files: `internal/module/machineid/machineid_test.go`)
  — verify: fails for the missing `windows` case.
- [x] T011 Implement: `SourceWindows`, the `MachineGUID` field, `guid_windows.go`
  (registry, `KEY_WOW64_64KEY`), `guid_other.go`, and the `windows` case in `Discover`.
  Add `guid_windows_test.go` reading the real value (FR-004). Update the source list in
  the `identity` help text. (files: `internal/module/machineid/*`, `internal/cli/cli.go`)
  — verify: T010 passes; `make all crosscheck`; owner pushes, `go-windows` green.

## Phase 2 — Logging and config path
- [x] T020 [P] Tests first for FR-025: `EventHandler` severity mapping, text and JSON
  bodies equal to the console line, attributes present, truncation, and `LineWriter`
  splitting. (files: `internal/winsvc/eventlog_test.go`) — verify: fails to compile
  (package missing).
- [x] T021 Implement `internal/winsvc/eventlog.go` (the handler, `Sink`, `LineWriter`) and
  `eventlog_windows.go` (a sink over `x/sys/windows/svc/eventlog`). Add `cli.App.Events`,
  with `newLogger` choosing the handler. (files: `internal/winsvc/*`,
  `internal/cli/logging.go`, `internal/cli/cli.go`) — verify: T020 passes; with `Events`
  nil, the existing cli tests pass unchanged.
- [x] T022 [P] FR-012: `config.Load` takes the probe path as a parameter, and
  `cli.App.DefaultConfig` feeds it. Tests: a missing service config gives defaults with no
  cwd probe, and an existing one is loaded. (files: `internal/config/config.go`,
  `config_test.go`, `internal/cli/cli.go`, `cli_test.go`) — verify: `make all`.

## Phase 3 — Service runtime
- [x] T030 FR-021–FR-023: `winsvc.IsService` (with a false stub off Windows) and
  `winsvc.Run` (a `svc.Handler` that accepts Stop, Shutdown and PreShutdown, cancels
  ctx, reports `StopPending` with checkpoints and maps the exit code). Add
  `run_windows_test.go`, which drives `Execute` through its channels. Wire the service
  branch into `main.go` with `DefaultConfig`, `Events` and the stderr `LineWriter`.
  (files: `internal/winsvc/run*.go`, `cmd/omnistat/main.go`) — verify: `make all
  crosscheck`; owner pushes, `go-windows` green.

## Phase 4 — Install & uninstall
- [x] T040 Tests first: `PlanInstall`/`PlanUninstall` plus apply against
  `winsvc/fakehost`, covering every row for FR-005–FR-011, FR-013, FR-014 and
  FR-017–FR-020 in the plan's testing table. Include the dry-run check (FR-028): the
  printed steps equal the applied steps, and no mutating call is made. (files:
  `internal/winsvc/fakehost/fakehost.go`, `install_test.go`, `uninstall_test.go`) — verify:
  fails to compile (planner missing).
- [x] T041 Implement `host.go` (the `Host` interface, `Installed`, `Registration`,
  errors), `install.go` and `uninstall.go` (planner, env merge, token resolution, steps),
  and `host_other.go` (`ErrUnsupported`). Write **ADR-0011** (the Windows service's security
  model) and index it. (files: `internal/winsvc/*`, `specs/decisions/0011-*.md`,
  `specs/decisions/README.md`) — verify: T040 passes; `make all specs-check`.
- [x] T042 Implement `host_windows.go`:
  - elevation, known folders, service query and parse of the binary path;
  - copy with temp file and rename; `MoveFileEx` deferral;
  - config dir DACL (protected SY/BA full, AU read and execute);
  - `mgr` create and update, recovery with the non-crash flag;
  - `Environment` write and protected key DACL (SY/BA only);
  - event source; start, stop and state polling;
  - no-echo console prompt.

  Add a Windows-only smoke test for anything that does not need elevation (known folders,
  prompt without a console returns `ErrNotInteractive`). (files:
  `internal/winsvc/host_windows.go`, `host_windows_test.go`) — verify: `make all
  crosscheck`; owner pushes, `go-windows` green.
- [x] T043 `internal/cli/service.go`: `service install [--dry-run] [--replace-token]` and
  `service uninstall [--dry-run]`, with `App.ServiceHost` for tests. Extract
  `identityCheck` from `identity.go` and use it as the pre-check with the merged `getenv`
  and the service config path. Update the usage text. Tests: pre-check messages equal to
  `identity`'s against `omnitest` (FR-006), the sentinel secret in no output (FR-016), and
  non-Windows → "not supported" with exit 1 (FR-027). (files: `internal/cli/service.go`,
  `service_test.go`, `identity.go`, `cli.go`) — verify: `make all crosscheck`.

## Phase 5 — Acceptance, docs, sync
- [x] T050 VM acceptance, run **by the owner** on their own `windows/amd64` VM against a
  dedicated project and throwaway token on the production API (NFR-005, and FR-001,
  FR-015, FR-024 and FR-026, which only a real host shows). It starts from a pre-release
  archive published by the release pipeline. Record each reported item in the spec's
  implementation notes:
  1. `identity` shows `windows-machine-guid`. `run --dry-run` and `schema plan` work in
     PowerShell (US-7/1).
  2. `service install --dry-run`, then install with the token at the prompt (US-1/1,2,6).
  3. `sc qc omnistat` shows the virtual account, delayed start and command line.
     `sc qfailure omnistat` shows three 60s restarts.
  4. As a standard user: `reg query …\Services\omnistat` is denied; the config directory
     and the binary cannot be written; `sc qc` still works (FR-015, NFR-001).
  5. Values in the project: `cpu` (usage, model, cores, arch) and `memory` (used %,
     available, total) read back and compared with Task Manager and `systeminfo`.
     `load_avg_*` is absent and reported once as unsupported.
  6. Event Viewer: source `omnistat`, info/warning/error at the right levels, debug
     records as info with `log.level: debug` (US-4).
  7. `Stop-Service` → final publish visible and the state is Stopped, not failed. Kill
     the process in Task Manager → restarted after 60s. A revoked token → error events,
     restart once a minute (US-3, US-4/2).
  8. Reboot → publishing resumes with nobody logged in, on the same entity (US-1/3, US-2/2).
  9. `run --daemon` in a console: Ctrl+C, Ctrl+Break, close the window (FR-024).
  10. Upgrade: install from a second snapshot → new version, no prompt, same entity.
      Run with `--replace-token`. Change `HTTPS_PROXY` (US-5).
  11. `service uninstall` run from the installed binary → deferred-deletion message; after a
      reboot nothing remains but the config directory; the project is untouched. Run
      again → "not installed", exit 0 (US-6).

  — verify: every item recorded, and any deviation fixed or accepted by the owner.
- [x] T051 [P] README: a Windows section covering install, upgrade, token rotation,
  uninstall, Event Viewer and the unsigned-binary note (NFR-006). Add CHANGELOG entries.
  (files: `README.md`, `CHANGELOG.md`) — verify: read-through by the owner.
- [x] T052 Sync:
  - 006 → `implemented`, with implementation notes.
  - Amend 002: FR-004, FR-007 and US-5 get the Windows source; drop "Windows discovery"
    from out of scope.
  - Amend 003: FR-020 references 006 FR-021/FR-024; drop "Windows" from out of scope.
  - Adjust the Windows notes in 004 and 005 "Out of scope".
  - Plan → `done`, tasks → `done`.

  (files: `specs/features/00{2,3,4,5,6}-*/…`) — verify: `make specs-check`.
