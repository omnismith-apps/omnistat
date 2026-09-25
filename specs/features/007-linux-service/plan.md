---
feature: 007-linux-service
status: done              # draft | approved | done
approved: 2026-09-25
spec: ./spec.md
created: 2026-09-25
depends_on: [003-run-loop-publisher, 006-windows]
---

# Plan: omnistat as a systemd service on Linux

## Constitution check
- [x] Uses the SDK for all API access (II). No new API operation. The install pre-check
  (FR-005) reuses `servicePrecheck`, which is the read-only path of `omnistat identity`.
- [x] Every template/attribute lives in exactly one module manifest with default slugs (III).
  No schema change.
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV). Uninstall
  deletes host-side files and the unit only, and makes no API call (FR-020).
- [x] No secret can reach a commit, a flag, or a log line (IV).
  - The token comes from the environment, the stored settings file or a no-echo prompt
    (FR-015), never from a flag.
  - Settings are printed by name only (FR-016).
  - The unit carries only the settings file's *path*. `EnvironmentFile=` content is not
    exposed by `systemctl show`.
  - A sentinel token and proxy password are asserted absent from every output, as in 006.
- [x] Every mutation is covered by dry-run (IV). Install and uninstall are one
  `service.Plan` value: `--dry-run` prints it, including the full unit text, and the real
  run applies it (FR-024).
- [x] Every FR/NFR has a test strategy using a fake API (V).
  - The Linux OS surface is one interface (`systemd.Host`, NFR-004) with an in-memory
    fake.
  - The real host's file operations are tested against `t.TempDir()`.
  - `systemctl` is exercised in the container acceptance run (NFR-005).
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI). Two new packages:
  - `internal/service`: the plan and settings logic shared by both platforms (FR-001,
    FR-013–FR-018, FR-024);
  - `internal/systemd`: the Linux backend and runtime integration (FR-001–FR-012,
    FR-019–FR-023).

  No new Go module: the terminal and `fstat` calls use `golang.org/x/sys/unix`, which is
  already required.

## Technical context
- Go 1.26, `CGO_ENABLED=0`, SDK `v1.0.15`, `golang.org/x/sys v0.41.0`, all pinned already.
- systemd facts checked on the dev host (systemd 259):
  - **Environment file quoting.** Single-quoted values are literal. Double-quoted values
    unescape `\"`, `\\`, `` \` `` and `\$`. Unquoted values lose trailing whitespace.
    Neither `$VAR` nor `%` specifiers are expanded. (Checked with `systemd-run --user
    -p EnvironmentFile=…`.) omnistat writes every value single-quoted, or
    double-quoted with escapes when it contains `'`.
  - **Exposure score.** `systemd-analyze security --offline=true` rates the unit below
    **1.1 OK**. What remains is inherent: the service needs `AF_INET`/`AF_INET6`,
    `AF_UNIX` (for the notify socket), the network, and `/proc` beyond the pid subset for
    `/proc/stat` and `/proc/meminfo`.
- Notify protocol (FR-010, FR-023): a datagram to `$NOTIFY_SOCKET`, a path or an
  `@`-prefixed abstract name. It carries `READY=1`, and on stop
  `STOPPING=1\nEXTEND_TIMEOUT_USEC=<µs>` (systemd ≥ 236). This is standard library
  `net.DialUnix("unixgram")`, without `go-systemd`.
- Journal detection (FR-022): `$JOURNAL_STREAM` is `<dev>:<ino>` of the stream systemd
  connected. It is compared with `fstat(2)` of stderr, so a redirected stderr is not
  mistaken for the journal. Priority prefix `<N>` at line start (`SyslogLevelPrefix=`
  defaults to on).
- `sd_booted()` equivalent (FR-004): `/run/systemd/system` is a directory.
- Unit introspection: `systemctl show omnistat.service -p LoadState -p FragmentPath -p
  ActiveState -p SubState -p DropInPaths`, which exits 0 even for an unknown unit
  (`LoadState=not-found`).

## Approach

**1. Extract what both platforms share into `internal/service`.** These move out of
`winsvc` with their tests, and their behaviour is unchanged:
- `Plan`/`Step`/`Describe`/`Apply`. `Step` gains `Detail`, printed indented by the
  dry-run only, so the full unit is shown (US-1/6). `Plan` gains `Unit` (printed when
  set) and `LogHint` (the "Logs: …" line).
- `Checked` and `InstallOptions`.
- `ResolveSettings`, the settings merge and prompts of 006 FR-013/014/017/018. It gains
  a list of names to capture (Linux adds the lower-case proxy variables, FR-013) and
  the project-id prompt (FR-017).
- `Watch`, the post-start guard, taking a check function so that each backend maps its
  own states.
- `ErrNotInteractive`, `ErrNoToken` and the `Prompter` interface (`PromptSecret`,
  `PromptLine`).

Afterwards, `winsvc` keeps its `Host`, `State`, the Windows plans, the Event Log and the
runner. `winsvc.Host` gains `PromptLine` (FR-017) and `CreateFile` for the starter
(FR-014).

**2. `internal/systemd`: the Linux backend**, modelled on `winsvc`:
- `host.go`: the seam (NFR-004):
  ```go
  type Host interface {
      Root() bool                                   // FR-002
      Booted() bool                                 // FR-004
      Executable() (string, error)
      Stat(path string) (File, error)               // File{Exists, Dir, Mode, UID}
      ReadFile(path string) ([]byte, error)
      WriteFile(path string, data []byte, mode fs.FileMode) error  // root-owned, temp+rename in the same dir
      CreateFile(path string, data []byte, mode fs.FileMode) (created bool, err error) // O_EXCL (FR-014)
      MkdirAll(path string, mode fs.FileMode) error
      Secure(path string, mode fs.FileMode) error   // chown 0:0 + chmod (FR-012)
      Remove(path string) error                     // absent is fine
      Unit(ctx context.Context) (Unit, error)       // systemctl show …
      Systemctl(ctx context.Context, args ...string) error
      service.Prompter
  }
  ```
  - `host_linux.go` is the real host: `os`, `x/sys/unix` termios, and `os/exec` of
    `systemctl`.
  - `host_other.go` returns `ErrUnsupported`.
  - `fakehost/` is an in-memory filesystem plus a `systemctl` call log.
  - Writing each file in its target directory (temp file, then rename) gives it that
    directory's default SELinux label (spec edge case).
- `unit.go`: `UnitText()` renders the unit. Its first line is the ownership marker
  (FR-019):
  ```ini
  # Written by `omnistat service install`; every install rewrites this file.
  # Local changes belong in a drop-in: systemctl edit omnistat
  [Unit]
  Description=omnistat (Omnismith exporter)
  Documentation=https://github.com/omnismith-apps/omnistat
  Wants=network-online.target
  After=network-online.target
  StartLimitIntervalSec=0                 # FR-009: no retry limit

  [Service]
  Type=notify                             # FR-010
  ExecStart=/usr/local/bin/omnistat --config /etc/omnistat/omnistat.yaml run --daemon
  EnvironmentFile=/etc/omnistat/omnistat.env
  Restart=on-failure                      # FR-009: exit≠0, signal, start timeout; not a clean stop
  RestartSec=60
  TimeoutStartSec=180
  DynamicUser=yes                         # FR-008
  CapabilityBoundingSet=
  AmbientCapabilities=
  NoNewPrivileges=yes
  ProtectSystem=strict
  ProtectHome=yes
  PrivateTmp=yes
  PrivateDevices=yes
  PrivateUsers=yes
  ProtectKernelTunables=yes
  ProtectKernelModules=yes
  ProtectKernelLogs=yes
  ProtectControlGroups=yes
  ProtectClock=yes
  ProtectHostname=yes
  ProtectProc=invisible
  RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
  RestrictNamespaces=yes
  RestrictRealtime=yes
  RestrictSUIDSGID=yes
  LockPersonality=yes
  MemoryDenyWriteExecute=yes
  SystemCallArchitectures=native
  SystemCallFilter=@system-service
  SystemCallFilter=~@privileged @resources
  SystemCallErrorNumber=EPERM             # a filtered call fails; it does not kill (Go's rlimit probe)
  UMask=0077
  RemoveIPC=yes
  DevicePolicy=closed

  [Install]
  WantedBy=multi-user.target
  ```
  (The real file has the comments on their own lines, because systemd has no trailing
  comments.) `TimeoutStopSec` stays at the default: the daemon extends it itself
  (FR-023, below).
- `envfile.go`: `FormatEnv(map) []byte` (sorted, quoted as above; a value containing a
  newline or NUL is rejected) and `ParseEnv([]byte) (map, error)`. The parser accepts
  what systemd accepts for the forms that matter: comments (`#`, `;`), blank lines,
  unquoted, single-quoted and double-quoted values. It errors with the line number
  only, never the content (spec edge case).
- `install.go` / `uninstall.go`: `PlanInstall(ctx, h, service.InstallOptions)` and
  `PlanUninstall(ctx, h)`. They are pure decisions over `Host` (below).
- `journal.go`: `NewJournalHandler(w, level, format)` formats each record as a console
  run would, then writes `<N>` + line (FR-022). `PriorityWriter(w, prio)` prefixes plain
  stderr lines, such as `fail()` messages and flag errors. `journal_linux.go` has
  `JournalStream(f *os.File, getenv) bool`; other OSes return false.
- `notify.go`: `Notify(getenv, state string) error`, a no-op without `$NOTIFY_SOCKET`.

**3. Install plan (Linux).** Decided before any change, in this order:
1. Check root (FR-002), then systemd (FR-004).
2. Read the unit state and ownership (FR-019):
   - ours: `/etc/systemd/system/omnistat.service` exists and starts with the marker;
   - foreign: that file exists without the marker, or systemd loads `omnistat.service`
     from another fragment (a package unit, `/run`, or a masked unit, which is reported
     as masked).
3. Read the stored settings from `omnistat.env` (upgrade), then
   `service.ResolveSettings` (FR-013, FR-015, FR-017, FR-018).
4. Pre-check with the resolved settings and `/etc/omnistat/omnistat.yaml` (FR-005).

Steps:

| # | Step (dry-run text, abridged) | When |
|---|---|---|
| 1 | stop the running service (final publish first) | upgrade and active |
| 2 | install/replace `<exe>` as `/usr/local/bin/omnistat` (root, 0755) | exe ≠ target |
| 3 | ensure `/etc/omnistat` (root, 0755) [+ "fix: owner uid N / mode 0777 → root 0755"] | always |
| 4a | create the starter `/etc/omnistat/omnistat.yaml` (root, 0644) | absent |
| 4b | fix `omnistat.yaml` owner/mode | present and loose |
| 5 | write `/etc/omnistat/omnistat.env` (root, 0600): *names* | always |
| 6 | write `/etc/systemd/system/omnistat.service` (0644), Detail = the unit text | always |
| 7 | `systemctl daemon-reload` | always |
| 8 | `systemctl enable omnistat.service` | always |
| 9 | `systemctl start omnistat.service`, then `Watch` (FR-011) | always |

"Loose" (FR-012) means one of these:
- not owned by uid 0;
- group- or other-writable;
- not other-readable (for a directory, also other-executable). The dynamic user is
  "other" and must be able to read.

A fix sets root:root with 0755 or 0644. Because the unit is `Type=notify`, step 9's
`systemctl start` blocks until `READY=1` or failure. A failure maps to `ErrStartFailed`
("see journalctl -u omnistat"). `Watch` then fails on `failed`, on `inactive`, or on
`activating/auto-restart` within the settle time.

**4. Uninstall plan (Linux).** Root, systemd, and unit ownership are checked the same
way. Not installed → `NotInstalled`. Foreign → error. Steps:
1. stop (if active);
2. `disable`;
3. `reset-failed` (errors ignored);
4. remove the unit;
5. `daemon-reload`;
6. remove `omnistat.env`;
7. remove `/usr/local/bin/omnistat`.

`Keep` lists `/etc/omnistat` (with `omnistat.yaml` if present) and each drop-in directory
from `DropInPaths` (FR-021).

**5. Windows amendments** (FR-014, FR-017):
- After "ensure config dir", `winsvc.PlanInstall` adds "create the starter
  `%ProgramData%\omnistat\omnistat.yaml`" when the file is absent. It is created with
  `O_EXCL` and inherits the directory's protected `OICI` DACL.
- The project-id prompt comes with the shared `ResolveSettings`.
- `windowsHost.PromptLine` reads a line when `GetConsoleMode` succeeds, and returns
  `ErrNotInteractive` otherwise.

**6. CLI.**
- `cli/service.go` chooses a backend: an injected `ServiceHost` (winsvc) or
  `SystemdHost` (systemd), or else by `a.goos()`: windows → `winsvc.NewHost`, linux →
  `systemd.NewHost`, otherwise the FR-003 message.
- A small unexported interface
  `installer{PlanInstall(ctx, service.InstallOptions); PlanUninstall(ctx)}` with two
  adapters keeps `serviceInstall`/`serviceUninstall` platform-neutral. The summary adds
  `unit:` when the plan has one and prints `p.LogHint` at the end.
- The usage text drops "Windows:".

**7. Runtime under systemd.**
- **Readiness (FR-010).** `run` calls `systemd.Notify(e.getenv, "READY=1")` in daemon
  mode, not in dry-run. The call comes after the schema is reconciled and the host is
  resolved, just before `loop.daemon()`. A notify error is logged at debug level and is
  not fatal.
- **Stop (FR-023).** When the loop leaves on a stop, before the final publish,
  `loop.daemon()` sends `STOPPING=1` and `EXTEND_TIMEOUT_USEC=(http.timeout + 5s)`. The
  stop timeout then follows whatever `http.timeout` the config holds *now*, with no unit
  edit. This is the systemd counterpart of 006's stop wait hints.
- **Journal (FR-022).** `main.go`: when `systemd.JournalStream(os.Stderr, os.Getenv)`
  is true, `app.Journal = os.Stderr` and the stderr passed to `Run` becomes
  `systemd.PriorityWriter(os.Stderr, 3)`. `App.newLogger` uses the journal handler when
  `Journal` is set, mirroring `Events` for Windows.

**8. Starter config (FR-014).** `internal/config/starter.yaml` is embedded as
`config.Starter`. Its convention:
- prose comment lines start with a capital letter;
- setting lines are `# key: value` with the YAML indentation kept after `# `;
- a blank line separates blocks.

Tests:
- the file as shipped loads to exactly the defaults;
- each block, uncommented alone, loads without error;
- every YAML key of `config.file` (found by reflection over the `yaml` tags, so a new
  setting cannot be forgotten) appears in the starter;
- the text contains the token warning and the restart note.

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/service` (new) | Plan/Step/dry-run, settings merge and prompts, pre-check result, start watch; shared by both backends | FR-001, FR-013–FR-018, FR-024; avoids a second copy of 006's logic |
| `internal/systemd` (new) | Linux backend: host seam, unit, env file, install/uninstall plans, journal handler, notify | FR-001–FR-012, FR-019–FR-023, NFR-004 |
| `internal/systemd/fakehost` (new, test support) | in-memory `systemd.Host` | NFR-004 |
| `internal/winsvc` | uses `service`; +`PromptLine`, `CreateFile`, starter step | FR-014, FR-017 |
| `internal/config` | +embedded `starter.yaml` | FR-014 |
| `internal/cli` | backend choice, `unit:`/log hint, readiness/stop notify, journal logger | FR-001, FR-003, FR-010, FR-022, FR-023 |
| `cmd/omnistat` | journal detection wiring | FR-022 |
| `scripts/e2e-systemd.sh` (new) | disposable systemd containers for NFR-005 | NFR-005 |

## Data flow
`sudo omnistat service install`:
1. The cli picks the systemd backend and runs `PlanInstall`:
   - root and systemd checks;
   - `Unit()`, then the marker check;
   - `ReadFile(omnistat.env)`, then `ParseEnv`;
   - `ResolveSettings` (environment, stored settings, prompts);
   - the pre-check, which reads the API with the SDK.

   The result is a `Plan`.
2. The cli prints the summary and then either:
   - `Describe` (dry-run), which prints each step and its Detail; or
   - `Apply`: host writes, then `systemctl`, then `Watch`.

At boot, systemd:
1. reads `omnistat.env` as root;
2. allocates the dynamic user;
3. execs `omnistat --config … run --daemon`.

The daemon then reconciles and resolves as in 003, sends `READY=1`, and publishes. Its
records go to stderr, which is the journal, with `<N>` prefixes. On SIGTERM it sends
`STOPPING=1` + `EXTEND_TIMEOUT_USEC`, makes the final publish and exits 0.

## Configuration
| Setting | Env var | Flag | Default | Requirement |
|---------|---------|------|---------|-------------|
| stored settings | `OMNISMITH_*`, `OMNISTAT_IDENTITY`, `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY` (+ lower case on Linux) | — | from the stored file | FR-013 |
| force token prompt | — | `--replace-token` | off | FR-018 |
| dry-run | — | `--dry-run` | off | FR-024 |
| readiness / journal | `NOTIFY_SOCKET`, `JOURNAL_STREAM` (set by systemd) | — | — | FR-010, FR-022 |

## Testing strategy
| Requirement | Test type | Where |
|-------------|-----------|-------|
| FR-001, FR-002, FR-004, FR-019, FR-020, FR-021 | unit (fakehost) | `internal/systemd/install_test.go`, `uninstall_test.go` |
| FR-005, FR-016, US-1/5 | cli + omnitest (pre-check message equals `identity`'s; sentinel secrets) | `internal/cli/service_linux_test.go` (fakehost, not build-tagged) |
| FR-003 | cli (GOOS darwin) | `internal/cli/service_test.go` |
| FR-006, FR-007, FR-008, FR-009, FR-010, FR-023 (unit text) | golden unit text | `internal/systemd/unit_test.go`, `testdata/omnistat.service` |
| FR-012 | unit (fakehost loose modes) + real host `Secure` when root | `install_test.go`, `host_linux_test.go` |
| FR-013 | table: quoting round-trip incl. `'`, `"`, `$`, `` ` ``, `%`, `\`; rejects newline | `internal/systemd/envfile_test.go` |
| FR-014 | starter loads to defaults; each block alone; key coverage | `internal/config/starter_test.go`; Windows step in `winsvc/install_test.go` |
| FR-015, FR-017, FR-018 | unit (fake prompter) | `internal/service/settings_test.go` |
| FR-010, FR-023 | daemon test with a `unixgram` listener as `NOTIFY_SOCKET`: `READY=1` after startup, `STOPPING=1`/`EXTEND_TIMEOUT_USEC` on cancel | `internal/cli/run_daemon_test.go` |
| FR-022 | handler priorities per level, json/text; `PriorityWriter`; `JournalStream` true on the matching dev:ino, false otherwise | `internal/systemd/journal_test.go` |
| FR-024 | dry-run: zero host calls, unit text shown, no secrets | `install_test.go`, cli test |
| real host files | `WriteFile` atomic + mode, `CreateFile` no-clobber, `ParseShow` | `internal/systemd/host_linux_test.go` |
| NFR-001, NFR-002, NFR-005 | container acceptance (agent), then the owner's runbook | `scripts/e2e-systemd.sh`, `acceptance.md` |
| NFR-006 | Windows VM, next rc | 006 runbook additions |

All moved `winsvc` tests keep passing where they move. Windows-only tests still run in
the Windows CI job. Locally, `GOOS=windows go vet` and the Windows lint pass are the gate.

**Container harness (NFR-005).** `scripts/e2e-systemd.sh <distro>` builds a small image
(the distro plus systemd) and starts it as a **disposable, privileged** container,
because systemd, DynamicUser, namespaces and seccomp need it. To keep the host
untouched, it:
- uses a bridge network, never `--network host`, so network sysctls stay in the
  container's namespace;
- masks every unit that could reach the shared kernel: `systemd-sysctl`, `udev`,
  `modules-load`, `binfmt`, time sync, `remount-fs`, `getty`, `hwdb`, `pstore`,
  `oomd`, `journald-audit`;
- is removed at the end.

The API is reached at `http://host.docker.internal:8100` (`--add-host …:host-gateway`).
The token is passed with `docker exec -e OMNISMITH_ACCESS_TOKEN`, from the git-ignored
`.env`, so it is never on a command line. The script drives the scenario list of
NFR-005/1 and prints PASS/FAIL per step. Its output is recorded in the spec's
implementation notes.

## Risks & unknowns
- **systemd 239 (Rocky 8) in a container on a cgroup-v2 host** may not boot. If so,
  RHEL 8 is accepted by unit-text review plus a note, and the spec records it.
  `ProtectProc`, `ProtectClock`, `ProtectKernelLogs` and `PrivateUsers` interplay are
  unknown or partial on 239, which merely warns.
- **Seccomp/Go runtime surprises** under `SystemCallFilter=~@resources` or
  `MemoryDenyWriteExecute`. `SystemCallErrorNumber=EPERM` avoids SIGSYS kills, and the
  container run on every distro catches the rest.
- **`PrivateUsers=yes` with `DynamicUser`** and reading `/etc/machine-id` or `/proc`:
  expected fine (world-readable), to be verified in the containers. If it breaks, it is
  dropped and the score is recorded.
- **Refactoring `winsvc`** touches Windows code that cannot run locally. It is kept
  mechanical (moves plus imports), gated by `GOOS=windows go vet` and the Windows lint,
  with CI on push, and it rides the next rc's VM run (NFR-006).
- **An existing hand-written unit from the README's old advice** is foreign by design.
  The README tells how to migrate: remove it, then install.

## Decisions taken here that deserve an ADR
- **ADR-0012: the Linux service's security model**, judged on ADR-0011's four points:
  - binary: `/usr/local/bin`, root-owned;
  - account: DynamicUser, no capabilities, sandboxed;
  - secrets: a root-only `EnvironmentFile`, not credentials, and why;
  - config: `/etc/omnistat`, root-writable only and readable by the service.

  Drafted with this feature, accepted at sync.
