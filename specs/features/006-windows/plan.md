---
feature: 006-windows
status: done              # draft | approved | done
approved: 2026-09-25
spec: ./spec.md
created: 2026-09-25
depends_on: [002-host-identity, 003-run-loop-publisher]
---

# Plan: omnistat on Windows — identity, service and logs

## Constitution check
- [x] Uses the SDK for all API access (II). No new API operation. The install pre-check
  (FR-006) reuses the read-only path of `omnistat identity`.
- [x] Every template/attribute lives in exactly one module manifest with default slugs (III).
  No schema change. `machine-id` gains a discovery source, not an attribute.
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV). Uninstall
  deletes host-side objects only (service, registry, event source, binary). It makes no
  API call (FR-020).
- [x] No secret can reach a commit, a flag, or a log line (IV). The token comes from the
  environment or a no-echo prompt (FR-014), never from a flag. Stored settings are printed
  by name only (FR-016); a test plants a sentinel token and asserts it appears in no output.
- [x] Every mutation is covered by dry-run (IV). `service install|uninstall --dry-run`
  print the same step list the real run executes (FR-028). Both come from one plan value.
- [x] Every FR/NFR has a test strategy using a fake API (V). The Windows OS surface is one
  interface (`winsvc.Host`, NFR-003) with an in-memory fake. Windows-only code has
  Windows-only tests that run in the new CI job (NFR-004).
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI). One new package, `internal/winsvc` (FR-005–FR-028). `golang.org/x/sys`
  moves from indirect to direct; it is already in `go.sum` via gopsutil. No new module.

## Technical context
- Go 1.26, `CGO_ENABLED=0`, SDK `v1.0.15`, `golang.org/x/sys v0.41.0`, all already
  pinned. Every API below was checked in the module cache at that version:
  - `windows/svc`: `IsWindowsService`, `Run`, `Handler`, `AcceptStop|AcceptShutdown|AcceptPreShutdown`.
  - `windows/svc/mgr`: `CreateService`, `Config{StartType, DelayedAutoStart, ServiceStartName, SidType}`,
    `UpdateConfig`, `SetRecoveryActions`, `SetRecoveryActionsOnNonCrashFailures`, `Control`, `Query`, `Delete`.
  - `windows/svc/eventlog`: `InstallAsEventCreate`, `Remove`, `Open`, `Info/Warning/Error`.
  - `windows/registry`, plus `windows.SetNamedSecurityInfo`, `SecurityDescriptorFromString`,
    `KnownFolderPath`, `GetCurrentProcessToken().IsElevated()`, `MoveFileEx`, and
    `GetConsoleMode`/`SetConsoleMode`.
- Console signals (FR-024): the Go runtime already maps Ctrl+C and Ctrl+Break to SIGINT,
  and console close, logoff and shutdown to SIGTERM. For SIGTERM it holds the process open
  while handlers run (checked in `runtime/os_windows.go` of the pinned toolchain). The
  existing `signal.NotifyContext` in `main.go` therefore covers FR-024 with no code, and the
  VM acceptance run confirms it. Windows kills a closed console after about 5s, which may cut
  the final publish short. That matches the spec's shutdown edge case.
- Machine GUID (FR-002): `HKLM\SOFTWARE\Microsoft\Cryptography`, value `MachineGuid`
  (`REG_SZ`), opened with `KEY_QUERY_VALUE|KEY_WOW64_64KEY`. Every user can read it. The
  existing `present()` already rejects a hyphenated all-zero value.

## Approach

**Identity (FR-002–FR-004).** `machineid.Module` gains `MachineGUID func() (string, error)`,
injectable like `IOReg`. `New()` binds it to `readMachineGUID`, a registry read in
`guid_windows.go` with an erroring stub in `guid_other.go`. `Discover` gets a `windows`
case with `SourceWindows = "windows-machine-guid"`. The value goes through `present()` and
`Derive()` unchanged, so ADR-0004's golden vector covers it. The error message names the
registry value it looked at, not its content.

**One seam for the OS (NFR-003).** `internal/winsvc` holds the whole feature. Every call
into Windows goes through one interface:

```go
type Host interface {
    Elevated() bool
    Executable() (string, error)
    ProgramDir() string                      // %ProgramFiles%\omnistat
    ConfigDir() string                       // %ProgramData%\omnistat
    Service(ctx) (Installed, error)          // exists?, binary path, state, stored env
    CopyBinary(src, dst string) error        // temp file + rename (replaces on Windows)
    RemoveBinary(path string) (deferred bool, err error) // MoveFileEx(DELAY_UNTIL_REBOOT) if in use
    EnsureConfigDir(path string) (tightened bool, err error)
    Register(ctx, Registration) error        // create or update config + recovery
    StoreEnv(ctx, map[string]string) error   // Environment value + restricted key DACL
    EventSource(install bool) error
    Start(ctx) error; Stop(ctx) error; State(ctx) (State, error)
    Delete(ctx) error
    PromptSecret(prompt string) (string, error) // ErrNotInteractive without a console
}
```

`host_windows.go` implements it with `x/sys`. `host_other.go` returns `ErrUnsupported`
from every method, so `omnistat service …` fails on Linux and macOS with the FR-027 message.
Tests use `winsvc/fakehost`, an in-memory `Host` that records calls. The same pattern
`omnitest` uses for the API.

**Install is a plan, then an apply (FR-005–FR-019, FR-028).** `winsvc.PlanInstall(inputs,
observed) (Plan, error)` is a pure function. It takes the elevation state, the running
binary path, the installed service if any, the stored environment, the process environment
and a forced-token flag. It returns an ordered `[]Step`, each with a human description and
an apply function bound to `Host`, plus the environment to store. `--dry-run` prints the
descriptions (FR-028), and the real run applies the same steps. So dry-run and apply can
never drift. The order:

1. Refuse if not elevated (FR-005) or if the service belongs to someone else (FR-019).
2. Resolve the stored environment. Start from the stored values, replace each captured
   variable present in the process environment (FR-013, FR-017), then settle the token:
   environment first; `--replace-token` or no token at all → `PromptSecret`; without a
   console → fail (FR-014, FR-018).
3. **Pre-check** (FR-006). The cli supplies a callback that runs the `identity` check
   with a `getenv` built from the resolved environment and the service config path. Any
   failure stops install before step 4. It is the same function `omnistat identity`
   calls, so the message is identical.
4. Stop the service if it is running (final publish, FR-017).
5. Copy the binary to `ProgramDir` unless it already runs from there (FR-007).
6. `EnsureConfigDir`. The DACL is protected: SYSTEM and Administrators full control,
   Authenticated Users read and execute. The config is not secret, and the service's
   virtual account is an authenticated user (FR-012).
7. `Register`: service `omnistat`, display name "omnistat (Omnismith exporter)",
   `StartAutomatic` + `DelayedAutoStart`, `ServiceStartName: "NT SERVICE\omnistat"` (a
   virtual account, with no password) and `SidType` unrestricted (FR-008, FR-009). The
   command line is `"<ProgramDir>\omnistat.exe" run --daemon`. Recovery is three
   `ServiceRestart` actions of 60s with a one-day reset, and
   `SetRecoveryActionsOnNonCrashFailures(true)` (FR-010). Without that flag Windows only
   restarts after a crash, not after a clean exit with a non-zero code (FR-023).
8. `StoreEnv`. The `Environment` `REG_MULTI_SZ` under
   `HKLM\SYSTEM\CurrentControlSet\Services\omnistat`, then a protected DACL on that key:
   SYSTEM and Administrators full control, nothing else (FR-013, FR-015). The service
   manager runs as SYSTEM, and nothing else needs to read the key.
9. `EventSource(true)`, via `InstallAsEventCreate`, supporting Info, Warning and Error (FR-026).
10. `Start`, then poll the state for 5s. Running → report and exit 0. Stopped → point to
    the Application log and exit 1 (FR-011).

`PlanUninstall` works the same way. It needs elevation, and "not installed" → message
and exit 0. The steps are: stop (final publish), delete the service (which removes the key
and the stored environment), remove the event source, then `RemoveBinary` (in use → deleted
at the next restart, said in the report), then the program directory if it is empty. The
config directory is kept (FR-020).

**Service runtime (FR-021–FR-023).** `main.go` asks `winsvc.IsService()`. If it is true,
it calls `winsvc.Run(func(ctx, sink) int)`, which wraps `svc.Run`. The handler accepts
Stop, Shutdown and PreShutdown. Any of them cancels `ctx`. The existing daemon then does
its final publish (003 FR-020). The handler meanwhile reports `StopPending` with a 5s wait
hint and advances `CheckPoint` every 2s until the cli returns. The manager therefore never
declares the service hung, however long `http.timeout` is (FR-021). PreShutdown gives up
to three minutes by default, instead of the shutdown notification's few seconds (FR-022).
The exit code maps to `(ssec=true, code)` when non-zero and to a clean stop when zero
(FR-023). `main.go` stays thin: one `if` plus the existing path.

**Config path for the service (FR-012).** The service starts in `System32`, so the
current `./omnistat.yaml` probe must not happen there. `cli.App` gains `DefaultConfig`,
the file used when `--config` is absent and only if it exists. `config.Load` takes the
probe path as a parameter instead of the `DefaultPath` constant. Console runs pass
`./omnistat.yaml` as today, and the service passes `<ConfigDir>\omnistat.yaml`.

**Logging (FR-025, FR-026).** `winsvc.EventHandler(sink, level, format)` is a
`slog.Handler`. It formats each record with the stock Text or JSON handler into a buffer,
strips the trailing newline and sends it to `sink.Info`, `Warning` or `Error` by level
(error → Error, warn → Warning, else Info). Event ids are 1, 2 and 3 for
Info/Warning/Error, and text longer than 31 000 characters is truncated with a marker.
`cli.App` gains `Events winsvc.Sink`. When it is set, `newLogger` builds this handler, and
`main` passes `winsvc.LineWriter(sink.Error)` as stderr. That way `fail()` messages and
pre-config errors reach the Application log too. When `Events` is nil, nothing changes.

**The `service` command.** `internal/cli/service.go`: `service install [--dry-run]
[--replace-token]` and `service uninstall [--dry-run]`, with the `Host` taken from
`App.ServiceHost` (nil → `winsvc.NewHost()`). The core of `identity` moves out of the
command into `identityCheck(e, s) (identityReport, error)`, which is called by both
commands.

**CI and the local gate (NFR-004).** `ci.yml` gains a `go-windows` job on
`windows-latest`: `go test ./...`, without race (no cgo toolchain assumed) and without
lint (Linux covers it). The Makefile gains `vet-windows` (`GOOS=windows go vet ./...`),
which type-checks every test file for Windows. `crosscheck` depends on it, so a
Windows-only compile error is caught locally before CI.

## Package layout (delta)
| Package / file | Purpose | Justified by |
|---|---|---|
| `internal/module/machineid` (+`guid_windows.go`, `guid_other.go`) | Windows discovery source | FR-002–FR-004 |
| `internal/winsvc` (new) | `Host` seam, install/uninstall plans, service runner, event-log handler | FR-005–FR-028, NFR-003 |
| `internal/winsvc/fakehost` (new, test support) | In-memory `Host` | NFR-003 |
| `internal/config` | Probe path as a parameter | FR-012 |
| `internal/cli` (+`service.go`) | `service` command; `identityCheck` shared; `DefaultConfig`, `Events`, `ServiceHost` | FR-005, FR-006, FR-025, FR-027 |
| `cmd/omnistat/main.go` | Service-mode branch | FR-021 |
| `.github/workflows/ci.yml`, `Makefile` | Windows test job, `vet-windows` | NFR-004 |

## Data flow

```
install:  cli ─flags─▶ winsvc.PlanInstall(observed Host state, env, --replace-token)
            │                 └─ env merge, token (env → prompt), foreign-service check
            ├─ precheck callback ─▶ identityCheck(getenv=merged, config=ConfigDir\omnistat.yaml)  [read-only API]
            ├─ --dry-run ─▶ print Step descriptions ─▶ exit 0
            └─ apply Steps in order ─▶ Host (x/sys) ─▶ start + 5s watch ─▶ exit 0/1
service:  SCM ─▶ omnistat.exe run --daemon ─▶ main: IsService ─▶ winsvc.Run
            └─ cli.App{DefaultConfig: ConfigDir\omnistat.yaml, Events: eventlog sink}
                 └─ daemon (003) … Stop/PreShutdown ─▶ ctx cancel ─▶ final publish ─▶ exit code ─▶ SCM
```

The service's environment reaches the process the way any environment does: the service
manager merges the `Environment` value into the process environment block at start.
`os.Getenv` is unchanged, and so is the precedence of flags, then environment, then config
file.

## Configuration
| Setting | Env var | Flag | Default | Requirement |
|---|---|---|---|---|
| Service config file | — | `--config` (console only) | `%ProgramData%\omnistat\omnistat.yaml` if present | FR-012 |
| Stored for the service | `OMNISMITH_ACCESS_TOKEN`, `OMNISMITH_PROJECT_ID`, `OMNISMITH_BASE_URL`, `OMNISTAT_IDENTITY`, `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` | — | from the installer's environment; kept if absent | FR-013, FR-017 |
| Force token prompt | — | `service install --replace-token` | off | FR-018 |
| Dry-run | — | `service install|uninstall --dry-run` | off | FR-028 |

## Testing strategy
| Requirement | Test type | Where |
|---|---|---|
| FR-002, FR-003 | unit, faked `MachineGUID` (value, empty, all-zero, error, derivation equals ADR-0004 vector for the same raw) | `machineid/machineid_test.go` |
| FR-002, FR-004 | Windows-only: the real registry read returns a non-zero GUID unprivileged | `machineid/guid_windows_test.go` (CI) |
| FR-005–FR-011, FR-013, FR-014, FR-017–FR-019 | unit: `PlanInstall` + apply against `fakehost` covering not elevated, pre-check failure (no calls made), fresh install order, run-from-installed-binary, upgrade merge, `--replace-token`, no console, foreign service, start then stop within 5s | `winsvc/install_test.go` |
| FR-016 | unit: sentinel token/proxy credentials never in stdout, stderr, dry-run or logs | `cli/service_test.go` |
| FR-020 | unit: uninstall order, not installed → 0, binary in use → deferred + message, config dir untouched | `winsvc/uninstall_test.go` |
| FR-006 | unit: install pre-check reuses `identityCheck` against the `omnitest` fake (401, 403, unreachable, no identity) with the same messages as `identity` | `cli/service_test.go` |
| FR-012 | unit: probe-path parameter; a missing service config gives defaults, no cwd probe | `config/config_test.go`, `cli/cli_test.go` |
| FR-021–FR-023 | Windows-only: handler fed Stop/PreShutdown → StopPending with advancing checkpoints → exit mapping | `winsvc/run_windows_test.go` (CI) |
| FR-025 | unit: severity mapping, text/json body equals the console line, attributes present, truncation, `LineWriter` | `winsvc/eventlog_test.go` |
| FR-027 | unit (non-Windows build): `service install` → "not supported on linux", exit 1 | `cli/service_test.go` |
| FR-028 | unit: dry-run output equals the step list and makes zero mutating `Host` calls | `winsvc/install_test.go` |
| FR-001, FR-015, FR-024, FR-026, NFR-001, NFR-005 | manual acceptance on the Windows VM (checklist in `tasks.md` T050) | — |
| NFR-004 | CI `go-windows` job green | `.github/workflows/ci.yml` |

## Risks & unknowns
- **The existing suite has never run on Windows.** It compiles there (`GOOS=windows go
  vet ./...` is clean today), but path, line-ending or timing assumptions may fail at
  runtime. T001 runs it first, before any feature code, so the failures that already exist
  are not mixed up with new ones. Only CI can run it, because no local Windows or Wine is
  available, so each round trip needs a push by the owner.
- **Service-key DACL.** Restricting `Services\omnistat` to SYSTEM and Administrators is
  expected to be invisible to the service manager, which runs as SYSTEM. `sc qc` still
  works for users because it goes through the manager, not the registry. Verified on the VM.
  Fallback if something needs read access: add the service SID only. The spec's promise
  (FR-015) does not change.
- **Environment merge semantics.** The service manager adds the `Environment` entries to
  the default environment; it does not replace it. That is well established but not verified
  here. If the VM showed otherwise (for example `SystemRoot` missing), install would also
  store `SystemRoot`. The acceptance run checks `run --dry-run` inside the service context.
- **Virtual account and the Application log.** Service accounts may write to the
  Application log by default. Verified on the VM. Without that permission, logging falls
  back to nothing, which would violate FR-025, so it is an acceptance blocker, not a
  footnote.
- **Acceptance is manual and remote.** The owner runs it on their own Windows VM against
  a dedicated project and throwaway token on the production API (decided 2026-09-25).
  The agent cannot observe it. The owner reports back, and the results are recorded in
  the spec. The
  build under test comes from the release pipeline as a pre-release tag (for example
  `v0.2.0-rc.1`), so the archive tested is the archive shipped.

## Decisions taken here that deserve an ADR
- **ADR-0011 — The Windows service's security model.** Virtual account; settings in the
  service's `Environment` behind a SYSTEM/Administrators-only key; binary under Program
  Files; config directory admin-writable; install and uninstall as a plan that dry-run
  prints. This is the Windows counterpart of a future systemd feature's `EnvironmentFile=`
  mode 0600, and it binds how that feature should be judged. Written in T041.
