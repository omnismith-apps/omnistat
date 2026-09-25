---
feature: 006-windows
status: implemented        # draft | review | approved | implemented | superseded
approved: 2026-09-25
implemented: 2026-09-25
created: 2026-09-25
owners: [evgenii]
supersedes: null
adr: [0004, 0007, 0010]
depends_on: [001-module-schema-reconciliation, 002-host-identity, 003-run-loop-publisher, 004-cpu-module, 005-memory-module]
---

# Feature: omnistat on Windows — identity, service and logs

## Summary

omnistat builds for Windows and its `cpu` and `memory` readings are specified there, but
it does not run there. It has no way to identify a Windows host, it does not stop cleanly
under the Windows service manager, and it has nowhere to log without a console. An
operator who wants it on a Windows host today has to pin an identity by hand and keep a
console window open.

This feature makes Windows a first-class host (ADR-0010). omnistat discovers a stable
identity from the OS. `omnistat service install` turns the downloaded binary into a
Windows service that starts at boot, survives reboots and crashes, stops without losing
buffered data, logs to the Windows Event Log, and keeps the access token away from other
users on the machine. Running `service install` again upgrades the binary or rotates the
token, and `service uninstall` removes everything that install created.

This feature amends 002 (Windows discovery, FR-004/FR-007, US-5) and 003 (stop handling,
FR-020). Those specs are edited when this one ships.

## Users & context

- **Windows operator**, often at a client and often not the person who set up the
  Omnismith project. Has the release zip, an elevated PowerShell, a token and a project
  id. Expects omnistat to behave like any other Windows service: it appears in Services,
  starts at boot without anyone logging in, logs to Event Viewer and is removed cleanly.
  Is not expected to have Go, git, a text editor for the registry, or knowledge of
  omnistat's internals.
- **Fleet operator** with a mixed fleet. Wants Windows hosts to appear as ordinary host
  entities with the same schema as Linux hosts, with the attributes Windows cannot supply
  left out (ADR-0007).
- Runs on Windows 10, Windows 11 and Windows Server 2016 or later (ADR-0010), on bare
  metal or in a VM, on amd64 (accepted) or arm64 (built only). Installing needs
  Administrator rights. Running does not. The host may reach the internet only through a
  proxy.

## User stories

### US-1 — Install once, then forget it (P1)
As a Windows operator, I want one command to turn the downloaded binary into a service
that runs from boot, so that the host reports without anyone logged in.

**Acceptance scenarios**
1. **Given** an elevated prompt with the token and project id in the environment,
   **When** I run `omnistat service install`, **Then** it checks the settings and the
   project without writing anything. It then installs the binary in a program directory,
   registers a service that starts automatically at boot, and starts it. It reports the
   service name, binary path, config path, the host identity and its source, and that the
   service is running.
2. **Given** no token in the environment and an interactive prompt, **When** I run
   install, **Then** I am asked for the token without it being echoed, and install
   continues as in 1.
3. **Given** an installed service, **When** the host reboots, **Then** omnistat starts
   without anyone logging in and publishes once it can reach the API.
4. **Given** a prompt that is not elevated, **When** I run install, **Then** nothing
   changes, the message says to run it as Administrator, and the exit status is 1.
5. **Given** a wrong token, an unreachable project or no discoverable identity, **When**
   I run install, **Then** nothing changes, the message is the one `omnistat identity`
   gives for the same problem, and the exit status is 1.
6. **Given** `--dry-run`, **When** I run install, **Then** it runs the same checks and
   prints every change it would make, without the token, and changes nothing.

### US-2 — A Windows host keeps one identity (P1)
As a fleet operator, I want a Windows host to find the same entity across restarts,
upgrades and renames, exactly like a Linux host.

**Acceptance scenarios**
1. **Given** a Windows host with no static identity, **When** I run `omnistat identity`,
   **Then** it prints a derived identity with source `windows-machine-guid` and never
   prints the raw OS value.
2. **Given** that host, **When** it reboots, omnistat is upgraded or reinstalled, or its
   hostname or IP changes, **Then** the identity and the host entity stay the same.
3. **Given** a static identity in config or `OMNISTAT_IDENTITY`, **Then** it wins, as
   on every platform (002 FR-005).

### US-3 — Stop, crash and shutdown without surprises (P1)
As a Windows operator, I want stopping, crashing and rebooting to behave like any
well-made service, so that data is not lost and a failure does not need me.

**Acceptance scenarios**
1. **Given** a running service with buffered observations, **When** I stop it from
   Services, `Stop-Service` or `sc stop`, **Then** it publishes what is buffered and is
   shown as stopped, not failed, within the HTTP timeout plus one second.
2. **Given** a running service, **When** its process ends unexpectedly or it exits with
   an error, **Then** Windows restarts it after one minute. A deliberate stop is never
   followed by a restart.
3. **Given** a running service, **When** Windows shuts down or restarts, **Then**
   omnistat attempts the same final publish as on a stop.

### US-4 — Logs where Windows admins look (P1)
As a Windows operator, I want omnistat's logs in Event Viewer, so that I can see why it
is not reporting without hunting for a file.

**Acceptance scenarios**
1. **Given** a running service, **When** it publishes, **Then** the Application log has
   events from source `omnistat`, with the same content a console run prints and with
   the severity matching the log level.
2. **Given** a revoked token, **When** the service starts, **Then** the reason is in the
   Application log as an error, and Services shows the service stopped.
3. **Given** `log.level: debug` in the service's config file, **When** the service
   runs, **Then** debug records appear in the Application log as information events.

### US-5 — Upgrade or rotate the token by installing again (P2)
As a Windows operator, I want to upgrade omnistat or change its token by running install
again, so that there is nothing extra to learn.

**Acceptance scenarios**
1. **Given** an installed service, **When** I run `service install` from a newer
   release, **Then** the installed binary is replaced, the service runs the new version,
   the stored settings are kept, no token is asked for, and the host entity is the same.
2. **Given** an installed service, **When** I run install with the option that replaces
   the token, **Then** I am asked for a token, it replaces the stored one, and the
   service restarts with it.
3. **Given** an installed service, **When** I run install with `HTTPS_PROXY` set to a
   new value, **Then** the stored proxy is replaced and every other stored setting is
   kept.

### US-6 — Uninstall cleanly (P2)
As a Windows operator, I want one command that removes what install created, so that no
service, token or event source is left behind.

**Acceptance scenarios**
1. **Given** an installed service, **When** I run `omnistat service uninstall`,
   **Then** the service stops (with its final publish), and the service, its stored
   settings including the token, the event source and the installed binary are removed.
   The config directory is kept, and nothing is written to the Omnismith project.
2. **Given** no installed service, **When** I run uninstall, **Then** it says so and
   exits 0.

### US-7 — Use omnistat from a Windows console (P2)
As a Windows operator, I want every command to work from PowerShell or `cmd` as it does
from a Linux shell, so that I can try things before installing the service.

**Acceptance scenarios**
1. **Given** the token and project id in the environment, **When** I run `schema plan`,
   `identity` or `run`, **Then** each behaves as specified in 001–005.
2. **Given** `run --daemon` in a console, **When** I press Ctrl+C or Ctrl+Break, or
   close the window, **Then** it behaves as 003 FR-020 specifies for SIGINT/SIGTERM.
3. **Given** Linux or macOS, **When** I run `omnistat service install`, **Then** it says
   the command is not supported on this platform and exits 1.

## Functional requirements

### Platform
- **FR-001** omnistat MUST run on Windows (`amd64`, `arm64`) with every existing command
  and module. The per-attribute platform declarations of 004 and 005 apply unchanged:
  for example, load averages are not collected on Windows (ADR-0007).

### Identity (amends 002 FR-004, FR-007, US-5)
- **FR-002** On Windows the `machine-id` provider MUST read the machine GUID that
  Windows keeps for the OS installation. The value is trimmed, and an empty or all-zero
  value counts as absent (002 FR-006 then applies).
- **FR-003** The published identity MUST be derived from it exactly as on Linux and macOS
  (002 FR-007, ADR-0004), with source `windows-machine-guid`. The raw GUID MUST NOT be
  published or logged (002 FR-018).
- **FR-004** Reading it MUST need no elevated privileges.

### `omnistat service install`
- **FR-005** `omnistat service install` MUST exist on Windows and MUST refuse to change
  anything unless it runs with Administrator rights (US-1/4).
- **FR-006** Before changing anything, install MUST perform the read-only checks of
  `omnistat identity`: the configuration is valid, the token and project are set, the
  project is reachable with that token, and an identity is available. On failure it
  changes nothing and reports the same message those commands give (US-1/5).
- **FR-007** Install MUST copy the binary it runs from into a per-machine program
  directory that only Administrators and SYSTEM can write, and MUST register that copy.
  A service MUST never run a binary from a location that other users can write to, such
  as a Downloads folder.
- **FR-008** Install MUST register a service named `omnistat`, with a display name and a
  description saying what it is. The service starts automatically at boot, after the
  boot-time services (delayed start), so the network is usually up. It runs the daemon of
  003 FR-019 with the service configuration of FR-012.
- **FR-009** The service MUST run under a dedicated, built-in, low-privilege service
  identity: not SYSTEM, not an administrator, and with no password to manage. It needs no
  write access anywhere on the host.
- **FR-010** Install MUST configure the service manager to restart the service one minute
  after any unexpected exit, every time. A deliberate stop MUST NOT cause a restart.
- **FR-011** Install MUST end by starting the service and reporting whether it reached
  the running state. If the service stops within its first few seconds, install MUST say
  so, point to the Application log, and exit 1.

### Configuration and secrets
- **FR-012** The service's config file is `omnistat.yaml` in an `omnistat` directory
  under the machine-wide application data folder (`%ProgramData%`). It is used when
  present, and defaults apply otherwise. Install MUST create that directory if it is
  missing, and MUST ensure that only Administrators and SYSTEM can write to it and that
  the service identity can read it. Install creates no config file. The config file can
  set `base_url`, so anyone able to write it could send the token to another server.
- **FR-013** Install MUST take these settings from its own environment and store them for
  the service, which receives them as its environment. Precedence of flags, then
  environment, then config file is unchanged:
  `OMNISMITH_ACCESS_TOKEN`, `OMNISMITH_PROJECT_ID`, `OMNISMITH_BASE_URL`,
  `OMNISTAT_IDENTITY`, `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`. The settings are
  stored with the service's own configuration, not in a file.
- **FR-014** The token MUST NOT be accepted from a flag (constitution IV). If it is
  neither in the environment nor already stored, install MUST ask for it without echo
  when run interactively. It MUST fail when not run interactively. A token typed into a
  PowerShell `$env:` assignment is saved in PowerShell's persistent command history, and
  the prompt is the way around that.
- **FR-015** The stored settings MUST be readable only by Administrators, SYSTEM and what
  the service manager needs to start the service. A standard local user MUST NOT be able
  to read them. Windows' default permissions on service configuration let every local
  user read it, so install MUST tighten them. This covers every stored setting, because a
  proxy URL can carry credentials.
- **FR-016** No output of install, uninstall or the dry-run, and no log line, may contain
  the value of any stored setting. They name the settings that are stored (003 FR-026).

### Installing again: upgrade and token rotation
- **FR-017** When an `omnistat` service installed by omnistat already exists, install
  MUST update it in place. It replaces the installed binary, updates the registration to
  FR-008–FR-010, replaces each stored setting that is present in its environment, keeps
  the ones that are absent, and ends with the service running (FR-011). The service stops
  (with its final publish, FR-021) before its binary is replaced.
- **FR-018** An explicit install option MUST force the token prompt (FR-014) even when a
  token is stored, and MUST replace the stored token.
- **FR-019** If a service named `omnistat` exists that omnistat did not install (its
  binary is not the one in the program directory of FR-007), install MUST change nothing
  and say so.

### `omnistat service uninstall`
- **FR-020** `omnistat service uninstall` MUST require Administrator rights. It MUST stop
  the service (FR-021), then remove the service registration, the stored settings, the
  event-source registration (FR-026) and the installed binary. It MUST keep the config
  directory and file. If the installed binary cannot be removed because it is the one
  running, uninstall MUST say exactly what remains and how to remove it. It MUST NOT
  leave the installed binary's own path scheduled for deletion: a reinstall before the
  next restart must keep its binary. *(Amended 2026-09-25: acceptance showed the running
  binary scheduled for deletion at restart in place, which would delete a binary
  reinstalled before that restart. The running binary is now moved aside first.)* With no
  installed service it says so and exits 0. It never touches the Omnismith project, and
  the host entity remains (constitution IV).

### Service lifecycle (amends 003 FR-020)
- **FR-021** A stop request from the service manager MUST be handled as 003 FR-020
  handles SIGTERM. omnistat stops scheduling, makes one final publish bounded by the
  HTTP timeout, and reports a clean stop. While it finishes, it MUST keep the service
  manager informed, so the service is not declared hung. 003 NFR-005 applies.
- **FR-022** When Windows shuts down or restarts, omnistat MUST attempt the same final
  publish. Windows may end the process sooner (see "Edge cases").
- **FR-023** When the daemon fails, at startup or later, it MUST report the failure to
  the service manager with a non-zero exit code. Services then shows it as stopped with
  an error, and FR-010 restarts it.
- **FR-024** In a console on Windows, Ctrl+C, Ctrl+Break and closing the console window
  MUST each have the effect 003 FR-020 gives SIGINT/SIGTERM, including "a second signal
  exits immediately".

### Logging (extends 003 FR-026…FR-028)
- **FR-025** When running as a service, every log record MUST go to the Windows
  Application log under event source `omnistat`, and nothing is written to stderr.
  Severity mapping: error → Error, warn → Warning, info and debug → Information. The
  event text is the line a console run prints in the configured format (`text` or
  `json`), with all structured attributes. Level and format come from the config file as
  for any run. Records produced before the configuration is loaded (for example, a config
  error) MUST also reach the Application log.
- **FR-026** Install MUST register the `omnistat` event source, and uninstall MUST remove
  it. Console runs keep logging to stderr (003).

### Elsewhere and dry-run
- **FR-027** On Linux and macOS, `omnistat service …` MUST fail with a message that the
  command is not supported on this platform, and exit 1 (US-7/3).
- **FR-028** `service install --dry-run` and `service uninstall --dry-run` MUST run the
  same checks and print every change they would make: paths, service settings, recovery
  policy, the names of stored settings, and permission changes. They change nothing
  (constitution IV).

## Non-functional requirements
- **NFR-001** (security) After install, nothing the service runs or reads can be
  changed by a non-administrator: the binary (FR-007), the config directory (FR-012) and
  the stored settings (FR-015). The service runs with low privileges (FR-009). A
  standard local user account on the acceptance host verifies all of this.
- **NFR-002** (resources) Running as a service adds nothing at runtime: memory and
  network behave as 003 specifies, and omnistat writes nothing to disk.
- **NFR-003** (testability) Everything except the calls into the Windows service manager,
  the stored settings, file permissions and the event log MUST be testable in `go test`
  on any OS. That includes deciding what install would do (the dry-run plan), choosing
  settings, the token prompt, the logging format and severity mapping, and GUID
  validation. The OS calls sit behind a narrow seam that tests replace.
- **NFR-004** (CI) The unit tests MUST run and pass on a Windows host in CI for every
  push and pull request, in addition to Linux (ADR-0010).
- **NFR-005** (acceptance) Proven by the owner, by hand, on a `windows/amd64` VM of
  their own (not the development host), against a **dedicated** project on the
  production Omnismith API with a token issued for this test only (it is revoked
  during the run). No client project is used. It starts from the release archive:
  - install, with the token entered at the prompt;
  - identity source `windows-machine-guid`, and the same entity after a reboot, an
    upgrade by re-install, and uninstall followed by install;
  - `cpu` (usage, model, cores, arch) and `memory` (used %, available, total) read back
    from the project and compared with the OS's own figures, verifying the Windows
    readings 004/005 only specified;
  - stop → final publish visible in the project;
  - reboot → publishing resumes with nobody logged in;
  - killing the process → restart after one minute;
  - Event Viewer shows the service's records at the right severities;
  - a standard user can neither read the token nor write the binary or the config
    directory;
  - uninstall leaves the project untouched and nothing of install behind except the
    config directory.

  `windows/arm64` is built and released, not accepted (ADR-0010).
- **NFR-006** (docs) The README MUST document Windows install, upgrade, token rotation,
  uninstall and where the logs are, including the unsigned-binary warning.

## Data & integration contract

Nothing new is read from or written to Omnismith. A Windows host resolves and publishes
exactly like a Linux host (002, 003). The `machine_id` attribute (module `machine-id`)
receives a value derived from a new source. The attributes Windows cannot supply are
still declared in the schema (ADR-0007). This feature adds no templates and no
attributes.

On the host, install creates:
- the program directory and the installed binary;
- the config directory;
- the `omnistat` service registration with its recovery policy and stored settings;
- the `omnistat` event source.

Uninstall removes all of these except the config directory.

## Edge cases & failure modes

- **VMs cloned from an image that was not generalised** share one machine GUID, and so
  share one identity and one entity. This is the Linux cloned-machine-id case of 002
  (US-2), and has the same remedy: pin a static identity per host. omnistat does not
  detect clones.
- **Machine GUID missing or empty** (a damaged or stripped image) → 002 FR-006: an
  actionable error suggesting a static identity. omnistat never generates an identity.
- **Network not up yet at boot**, even with delayed start: the daemon fails like any
  startup failure (003), logs why, and the recovery policy retries one minute later.
- **Revoked token**: every start fails, once a minute, each time with an error in the
  Application log. The rate is bounded by FR-010, and nothing is written.
- **Shutdown shorter than the final publish**: Windows gives services a limited time at
  shutdown. If the final publish does not finish in time, what was buffered since the
  last publish (at most one publish interval) is lost. This is accepted and documented.
- **The service manager's stop wait is shorter than the HTTP timeout** (for example, an
  operator set `http.timeout` very high): FR-021 keeps the manager informed so the stop
  still completes. The final publish is bounded by the HTTP timeout, as on Linux.
- **Install run from the installed binary itself** (for example, to rotate the token):
  there is nothing to copy. Install updates the registration and settings only.
- **Service marked for deletion** (it was removed while the Services console held it
  open): install reports that the Services console must be closed or the host restarted,
  and changes nothing.
- **Existing config directory with loose permissions** (created by hand earlier):
  install tightens them (FR-012) and says so. Dry-run shows the change.
- **Authenticating proxy**: the credentials sit in the proxy URL and are protected like
  the token (FR-015, FR-016).
- **Unsigned binary**: SmartScreen may warn when the operator first runs the downloaded
  binary. The service itself runs the installed copy and is not interactive. Code
  signing is out of scope.
- **API unreachable / 401 / 403 / 404 / 422 / 429 while running**: unchanged from
  001–003.

## Out of scope

- Code signing, and MSI, winget or Chocolatey packages.
- Service installation on Linux (systemd) and macOS (launchd). That is a later feature,
  and `service` is Windows-only until then (FR-027).
- `start`, `stop` and `status` subcommands. Services, `Start-Service`/`Stop-Service` and
  `sc` already do this.
- Several instances on one host, or custom service names.
- Windows-specific values (processor queue length, performance counters, disks, event
  log contents). Each belongs to its own module spec.
- Domain-wide deployment tooling (Group Policy, SCCM). Install is scriptable
  non-interactively with the token in the environment, and that is the limit.
- Windows versions older than Windows 10 / Server 2016. Accepting `windows/arm64`.
- Persisting the buffer across restarts (003).

## Decisions taken during review (2026-09-25)

Chosen by the owner:
- Service installation is a built-in `omnistat service install|uninstall`, not a
  documented `sc.exe` recipe.
- The token lives in the service's own stored environment, not a file, so constitution
  IV ("secrets from the environment") holds without amendment.
- Logs go to the Windows Event Log, not to a log file.
- Service management is Windows-only in this feature.
- Acceptance runs on a Windows VM before any client host. The owner runs it by hand on
  their own VM, against a dedicated project and token on the production API. The
  development host has no Windows VM and no local-API access from one (decided
  2026-09-25).

Proposed while drafting and approved by the owner, who confirmed the configuration, token
and permission scope with the first client:
- **Install copies itself into a program directory** (FR-007). A service registered
  against a user-writable path is a privilege-escalation hole, and a fixed location
  makes "install again" the upgrade path (US-5).
- **The token's protection must be tightened explicitly** (FR-015). The owner chose the
  stored service environment assuming only administrators could read it. Windows' default
  permissions on service configuration let every local user read it, so install has to
  restrict them. The acceptance run verifies this with a standard user account.
- **The config directory is admin-writable only** (FR-012). `base_url` in the config
  file would otherwise let any local user redirect the token.
- **Low-privilege service identity** (FR-009). omnistat's readings need no privileges
  (004/005 NFR-003, FR-004 here), and it writes nothing at runtime.
- **Delayed automatic start, and a restart one minute after any failure** (FR-008,
  FR-010). This is the Windows equivalent of `Restart=on-failure`. One minute keeps a
  revoked token from hammering the API.
- **Install checks before it changes anything** (FR-006). A bad token then shows up at
  the prompt, not later in Event Viewer. It means install needs network access to the
  API.
- **Proxy variables are carried into the service** (FR-013). A service does not see the
  logged-in user's proxy settings, and at a client the proxy is often mandatory.
- **The token is prompted for, not typed into `$env:`** (FR-014), because of PowerShell's
  persistent history.

## Open questions

None.

## Implementation notes (2026-09-25)

All tasks are done. `make all crosscheck` is green, and golangci-lint is clean for the
Windows build too.

**Acceptance (T050, NFR-005)** was run by the owner on 2026-09-25, on a `windows/amd64` VM
(Windows with a Russian UI; 2 × Xeon E5645, 24 logical CPUs, 16 GiB), against a dedicated
production project, from `v0.1.0-rc.1`. The owner confirmed it as a whole. Recorded:

- **Step 1:** identity `windows-machine-guid`, stable throughout. The `load_avg_*`
  attributes were skipped once each. The readings were consistent: 24 cores,
  16374 MiB total, 49.31% used, 8299 MiB available. The dry-run's `cpu_arch` error was
  a 003 bug (below).
- **Step 2:** closing the console window stopped the publishing. Whether the final
  publish landed before Windows ended the process was not established; the spec accepts
  that loss.
- **Steps 3–5:**
  - refused without elevation (exit 1);
  - dry-run identical to the real install, with no token shown and nothing installed;
  - installed and running, with Event Log entries and data arriving.
- **Step 6:** `sc qc` showed `AUTO_START (DELAYED)`, the quoted binary with
  `run --daemon`, and `NT SERVICE\omnistat`. `qfailure` showed three 60000 ms restarts
  with an 86400 s reset, and `qfailureflag` was TRUE. The stored setting names were
  `OMNISMITH_ACCESS_TOKEN` and `OMNISMITH_PROJECT_ID`. The config directory had SYSTEM,
  Administrators and Authenticated Users.
- **Step 7 (FR-015, NFR-001):** as the standard user `omnistd`:
  - `reg query` on the service key was **denied**;
  - `sc qc` worked;
  - writing `omnistat.yaml` and creating a file in the config directory were **denied**;
  - replacing the installed binary was **denied**.
- **Steps 8–14:** confirmed: values, Event Viewer, stop (0.5 s, clean, no restart
  after a deliberate stop), crash restart, revoked token, token rotation and reboot. The
  Application log is readable by every local user, as Windows intends; omnistat logs no
  secrets (FR-016).
- **Steps 15–16:** upgrade confirmed. Uninstall stopped the service, whose final publish
  and summary were logged, and removed it. The in-use binary was reported as removed at
  the next restart; the owner could not reboot to see it go. That report exposed the
  FR-020 bug below.

Found in acceptance and fixed after it:

- **FR-020, uninstall from the installed copy.** The running binary was scheduled for
  deletion at restart **at its installed path**. A reinstall before that restart would
  have lost its fresh binary at boot. Now the running binary is moved to
  `%SystemRoot%\Temp` (or, failing that, renamed next to itself). The program directory
  is removed at once, and only the moved file is deleted at restart. The owner has not
  run this fix on Windows yet: **to verify in the next rc** (runbook step 16).
- **Service key ACL shows `ALL APPLICATION PACKAGES` (read)** next to SYSTEM and
  Administrators, although install writes a protected SYSTEM/Administrators-only DACL.
  It is not a gap. An AppContainer process needs its user **and** its package granted,
  and no user other than SYSTEM or an administrator is granted, which step 7 confirmed.
  Where the entries come from was not established. The runbook now expects them.
- **Runbook:** `New-Item` needs `-ItemType File` on the owner's PowerShell version.

Deviations from the plan:

- **Pre-check and an empty project (FR-006).** `omnistat identity` fails on a project whose
  schema is not applied yet ("schema is not ready"). The install pre-check accepts that
  case and reports "the service applies it when it starts": the service reconciles on
  start, so a first install into an empty project must not be refused. Every other
  resolution error (401, 403, no project, schema conflict, no identity) still stops
  install with `identity`'s exact message. `TestServiceInstall_PrecheckMatchesIdentity`
  compares the two. Unlike `identity`, the pre-check also requires the token and the
  project to be set.
- **Config probe.** `config.Load` kept its signature. `config.LoadWithDefault(path, probe,
  getenv)` was added, and `Load` calls it with `./omnistat.yaml`. The cli commands load
  through one `env.loadConfig()`.
- **Test knob.** `cli.App.ServiceSettle` shortens install's 5s post-start watch in tests,
  like `Clock` and `MaxPerMetric`.
- **`Host.ProgramDir`/`ConfigDir` return an error.** Both resolve a Windows known folder,
  which can fail.
- **Beyond the plan:**
  - `.gitattributes` forces LF, because Git for Windows checks out CRLF by default and the
    Windows CI job would otherwise compare CRLF testdata.
  - `make lint` and CI also lint the Windows build (`GOOS=windows`), because the host lint
    skips `_windows.go` files.
  - `scripts/release-notes.sh` lets a pre-release tag use the *Unreleased* section, so the
    acceptance build can be a published `-rc` without a changelog edit.
- **Found in acceptance step 1 (not Windows-specific):** `run --dry-run` on an empty
  project logged `observation dropped` for `cpu_arch`, because the option the plan would
  create had no item id yet. Fixed in spec 003 (FR-021 amended), shipping in
  `v0.1.0-rc.2`. The real run was never affected.
- **Found in acceptance step 5 (owner feedback):** one `published` info line per minute
  crowded the Windows Application log. Spec 003 gained FR-026a: in daemon mode, publishes
  are logged at debug, with the first publish, recoveries and a `publish summary` every
  `log.summary_interval` (default 15m, `0` = old behaviour) at info. Ships in
  `v0.1.0-rc.2`.
- **Console stop keys (FR-024)** need no code: the Go runtime turns Ctrl+C and Ctrl+Break
  into SIGINT and closing the console into SIGTERM (plan, "Technical context"). The VM run
  verifies it.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md` (1.1.0, ADR-0010 accepted)
