---
feature: 007-linux-service
status: implemented        # draft | review | approved | implemented | superseded
approved: 2026-09-25
implemented: 2026-09-25
created: 2026-09-25
owners: [evgenii]
supersedes: null
adr: [0010, 0011, 0012]
depends_on: [002-host-identity, 003-run-loop-publisher, 006-windows]
---

# Feature: omnistat as a systemd service on Linux

## Summary

On Windows, `omnistat service install` turns the downloaded binary into a hardened
service with one command (006). On Linux, which is omnistat's primary target, there is no
such command. An operator has to write a unit file by hand, decide where the token goes,
and work out how to keep it away from other users. Each host ends up set up differently,
and the README cannot describe one way to do it.

This feature brings `omnistat service install|uninstall` to Linux hosts that run systemd.
One `sudo` command installs the binary in a system location, creates `/etc/omnistat`
with a commented starter config, stores the token where only root can read it, and
registers a modern, sandboxed systemd unit. The service runs as an unprivileged,
ephemeral user with a read-only view of the system. It starts at boot once the network
is online, restarts after failures, tells systemd when it is ready, and logs to the
journal at the right priorities. Installing again upgrades the binary or rotates the
token, and uninstall removes what install created. The commands, flags, output and
README section match Windows, so a mixed fleet has one procedure.

This feature also amends 006 in two small ways, so the platforms stay consistent: install
writes a starter config file on Windows too (FR-014), and install asks for a missing
project id on both platforms (FR-017).

## Users & context

- **Linux admin**, often at a client and often not the person who set up the Omnismith
  project. Has the release tarball, `sudo`, a token and a project id. Expects omnistat to
  behave like any well-packaged daemon:
  - `systemctl status omnistat` and `journalctl -u omnistat` work;
  - the config is in `/etc`;
  - the service does not run as root;
  - nothing is left behind after removal.

  Is not expected to write unit files or know systemd's sandboxing options.
- **Fleet operator** with a mixed Linux/Windows fleet. Wants the same install, upgrade,
  rotate and uninstall steps and the same documentation on both.
- **Platforms**:
  - **Linux** with systemd as the init system, version 239 or later. That covers RHEL,
    Rocky and Alma 8+, Debian 11+, Ubuntu 20.04+ and Fedora. On bare metal, VMs or
    systemd-booted containers, amd64 (accepted) and arm64 (built). Installing needs
    root (`sudo`); running does not.
  - **Hosts without systemd** (Alpine/OpenRC, a container without an init system): not
    supported. Install says so (FR-004).
  - **macOS**: out of scope. `service` keeps saying it is not supported there (006
    FR-027).
  - **Windows**: behaviour unchanged except the two amendments above.

## User stories

### US-1 — Install once, then forget it (P1)
As a Linux admin, I want one command to turn the downloaded binary into a system service
that runs from boot, so that the host reports without anyone logged in.

**Acceptance scenarios**
1. **Given** a shell with `sudo` rights, **When** I run
   `sudo ./omnistat service install` and type the project id and token at the prompts,
   **Then** it checks the settings and the project without writing anything. It then
   installs the binary in a system location, creates `/etc/omnistat` with a commented
   `omnistat.yaml`, stores the settings root-only, registers and enables the `omnistat`
   unit, and starts it. It reports the unit, binary path, config path, the host identity
   and its source, and that the service is running.
2. **Given** the token and project id passed through `sudo` (for example
   `sudo --preserve-env=OMNISMITH_ACCESS_TOKEN,OMNISMITH_PROJECT_ID`), **When** I run
   install, **Then** nothing is asked, so install can be scripted.
3. **Given** an installed service, **When** the host reboots, **Then** omnistat starts
   after the network is online, without anyone logging in, and publishes.
4. **Given** I am not root, **When** I run install, **Then** nothing changes, the message
   says to run it with `sudo`, and the exit status is 1.
5. **Given** a wrong token, an unreachable project or no discoverable identity, **When**
   I run install, **Then** nothing changes, the message is the one `omnistat identity`
   gives for the same problem, and the exit status is 1.
6. **Given** `--dry-run`, **When** I run install, **Then** it runs the same checks and
   prints every change it would make, including the full unit file, without any setting
   value, and changes nothing.

### US-2 — A service that cannot hurt the host (P1)
As a Linux admin, I want the service to run with the least privilege systemd can give,
so that a compromised or buggy omnistat can read what it reports and nothing more.

**Acceptance scenarios**
1. **Given** a running service, **When** I look at its process, **Then** it runs as an
   unprivileged user that exists only while the service runs, with no capabilities, and
   it cannot write anywhere on the filesystem.
2. **Given** an installed unit, **When** I run `systemd-analyze security omnistat`,
   **Then** the overall exposure is rated "OK" or better (NFR-002).
3. **Given** a running service, **Then** it publishes the same identity, `cpu` and
   `memory` values as `omnistat run` started by hand on that host.

### US-3 — Stop, crash and shutdown without surprises (P1)
As a Linux admin, I want stopping, crashing and rebooting to behave like any well-made
daemon.

**Acceptance scenarios**
1. **Given** a running service with buffered observations, **When** I run
   `systemctl stop omnistat`, or the host shuts down, **Then** it publishes what is
   buffered and stops cleanly (003 FR-020). `systemctl status` shows it inactive, not
   failed.
2. **Given** a running service, **When** its process is killed or exits with an error,
   **Then** systemd starts it again after one minute, as many times as needed. A
   deliberate stop is never followed by a restart.
3. **Given** a revoked token, **When** the service starts, **Then** `systemctl start`
   reports the failure, because the service never becomes ready. The reason is in the
   journal, and the next attempt comes one minute later.

### US-4 — Logs where Linux admins look (P1)
As a Linux admin, I want omnistat's logs in the journal with proper priorities, so that
`journalctl -u omnistat -p warning` shows exactly its problems.

**Acceptance scenarios**
1. **Given** a running service, **When** it publishes, **Then**
   `journalctl -u omnistat` shows the same lines a console run prints.
2. **Given** a warning or an error, **Then** the journal records it at priority warning
   or err. Info is recorded at info and debug at debug.

### US-5 — Upgrade or rotate the token by installing again (P2)
As a Linux admin, I want to upgrade omnistat or change its token by running install
again, exactly as on Windows.

**Acceptance scenarios**
1. **Given** an installed service, **When** I run `sudo ./omnistat service install` from
   a newer release, **Then** the installed binary and the unit are replaced, the stored
   settings and my `omnistat.yaml` are kept, no token is asked for, the service runs the
   new version, and the host entity is the same.
2. **Given** an installed service, **When** I run install with `--replace-token`,
   **Then** I am asked for a token, it replaces the stored one, and the service restarts
   with it.
3. **Given** an installed service, **When** I run install with `HTTPS_PROXY` passed
   through `sudo`, **Then** the stored proxy is replaced and every other stored setting
   is kept.
4. **Given** a drop-in I added with `systemctl edit omnistat`, **When** I run install
   again, **Then** my drop-in is untouched and still applies.

### US-6 — Uninstall cleanly (P2)
As a Linux admin, I want one command that removes what install created.

**Acceptance scenarios**
1. **Given** an installed service, **When** I run `sudo omnistat service uninstall`,
   **Then** the service stops (with its final publish), and the unit, the stored
   settings including the token and the installed binary are removed. `/etc/omnistat`
   with `omnistat.yaml` is kept, and nothing is written to the Omnismith project.
2. **Given** no installed service, **When** I run uninstall, **Then** it says so and
   exits 0.
3. **Given** `--dry-run`, **When** I run uninstall, **Then** it prints what it would
   remove and what it would keep, and changes nothing.

### US-7 — The same starter config on Windows (P3, amends 006)
As a Windows operator, I want install to leave a commented `omnistat.yaml` in the config
directory, as on Linux, so that I can see what can be configured without the README.

**Acceptance scenarios**
1. **Given** no `%ProgramData%\omnistat\omnistat.yaml`, **When** I run install, **Then**
   a commented starter file is created there, with the directory's permissions (006
   FR-012). The service behaves exactly as with no file.
2. **Given** an existing `omnistat.yaml`, **When** I run install, **Then** it is not
   changed.

## Functional requirements

### Platform and preconditions
- **FR-001** `omnistat service install|uninstall` MUST work on Linux hosts whose init
  system is systemd 239 or later, with the flags of 006: `install [--dry-run]
  [--replace-token]` and `uninstall [--dry-run]`.
- **FR-002** Install and uninstall, including `--dry-run`, MUST refuse to change anything
  unless they run as root. The message says to run them with `sudo`. *(A dry-run needs
  root because it reads the root-only stored settings, 006 parity.)*
- **FR-003** On macOS, `service …` MUST keep failing as 006 FR-027 specifies. The message
  no longer says the command is Windows-only.
- **FR-004** When systemd is not the running init system, install and uninstall MUST
  change nothing and say that the service commands need systemd, and that
  `omnistat run --daemon` can be run under another supervisor.

### `omnistat service install`
- **FR-005** Before changing anything, install MUST perform the read-only checks of
  006 FR-006, including its "empty project" deviation. The checks cover the
  configuration, token and project, reachability and identity. They use the settings the
  service will get (FR-013) and the config file it will read (FR-012). On failure,
  install changes nothing and reports the message those checks give.
- **FR-006** Install MUST copy the binary it runs from to `/usr/local/bin/omnistat`,
  owned by root and not writable by anyone else, and MUST register that copy. The unit
  MUST never run a binary from a location that another user can write to. When install
  runs from the installed copy, there is nothing to copy.
- **FR-007** Install MUST write the unit `/etc/systemd/system/omnistat.service`, marked
  as written by omnistat and rewritten by every install. The unit MUST:
  - have a description saying what it is and a documentation link;
  - start after the network is online, and be wanted by the normal multi-user boot;
  - run the daemon of 003 FR-019 with the config file of FR-012;
  - be enabled, so that it starts at boot.
- **FR-008** The service MUST run as an unprivileged user that systemd allocates for the
  service while it runs. There is no user account to create, manage or remove, and it is
  not root. The service MUST have no capabilities, MUST be unable to gain privileges, and
  MUST see the filesystem read-only, with home directories hidden. It gets systemd's
  standard kernel, device, namespace and system-call restrictions, as far as they do not
  prevent reading what the enabled modules report.
- **FR-009** The unit MUST have systemd restart the service one minute after any
  failure: a non-zero exit, a crash, a kill, or never becoming ready. This happens every
  time, with no retry limit. A deliberate stop MUST NOT cause a restart.
- **FR-010** The service MUST tell systemd when it is ready: after its configuration is
  loaded, the schema is reconciled and the host identity is resolved, before the first
  collection. A start that fails before that point is a failed start. Outside systemd
  this has no effect.
- **FR-011** Install MUST end by starting the service (or restarting it on an upgrade)
  and reporting whether it became ready and kept running for a few seconds. If it did
  not, install MUST say so, point to `journalctl -u omnistat`, and exit 1.

### Configuration and secrets
- **FR-012** The service's config file is `/etc/omnistat/omnistat.yaml`. Install MUST
  create `/etc/omnistat` if missing, owned by root and writable only by root. The config
  file can set `base_url`, so anyone able to write it could send the token to another
  server. When the directory or an existing config file is writable by anyone but root,
  or not owned by root, install MUST fix that and say so. Dry-run shows the change.
- **FR-013** Install MUST take the settings of 006 FR-013 from its own environment and
  store them for the service, which receives them as its environment. These are
  `OMNISMITH_ACCESS_TOKEN`, `OMNISMITH_PROJECT_ID`, `OMNISMITH_BASE_URL`,
  `OMNISTAT_IDENTITY` and the proxy variables `HTTPS_PROXY`, `HTTP_PROXY` and
  `NO_PROXY`. On Linux, the lower-case proxy spellings are accepted and stored as they
  are. The settings MUST be stored in one file, `/etc/omnistat/omnistat.env`, that only
  root can read. systemd reads it before it drops privileges. They MUST NOT be stored in
  the unit, a drop-in, or anything else that `systemctl show` reveals to other users.
  Precedence of flags, then environment, then config file is unchanged.
- **FR-014** Install MUST create a commented starter `omnistat.yaml` in the config
  directory when none exists, on Linux (root-owned, world-readable) and on Windows
  (amends 006 FR-012; it gets the directory's permissions). The starter file:
  - leaves every default in force;
  - shows each setting of the config file with its default value and a one-line
    explanation;
  - is valid when a setting is uncommented alone;
  - says that the token never goes in this file;
  - says that the service must be restarted after an edit.

  An existing file is never changed. Uninstall keeps it (FR-021).
- **FR-015** The token MUST NOT be accepted from a flag (constitution IV). If it is
  neither in the environment nor already stored, install MUST ask for it without echo
  when a terminal is attached, and MUST fail when none is (006 FR-014). `sudo` drops the
  caller's environment by default, so the prompt is the expected path on Linux.
- **FR-016** No output of install, uninstall or the dry-run, and no log line, may contain
  the value of any stored setting (006 FR-016). They name the settings that are stored.
- **FR-017** If the project id is set neither in the environment, nor in the stored
  settings, nor in the config file, install MUST ask for it (with echo; it is not a
  secret) when a terminal is attached, and store it with the other settings. Without a
  terminal it fails as the checks of FR-005 do. This applies on Windows too (amends 006).

### Installing again
- **FR-018** When a unit installed by omnistat already exists, install MUST update in
  place:
  - stop the service (with its final publish, 003 FR-020);
  - replace the installed binary and the unit;
  - replace each stored setting present in its environment, and keep the ones that are
    absent;
  - keep the config file and any drop-ins;
  - end with the service running (FR-011).

  `--replace-token` forces the token prompt and replaces the stored token (006 FR-018).
- **FR-019** If an `omnistat` unit exists that omnistat did not write, install MUST
  change nothing and say where that unit is. That covers a hand-written unit or one from
  a package, whether in `/etc` or elsewhere on the unit search path.

### `omnistat service uninstall`
- **FR-020** Uninstall MUST require root. It MUST stop and disable the service, then
  remove the unit, the stored settings file and the installed binary, and make systemd
  forget the unit. With no installed unit, it says so and exits 0. With a unit omnistat
  did not write (FR-019), it changes nothing and says so. It never touches the Omnismith
  project, and the host entity remains (constitution IV).
- **FR-021** Uninstall MUST keep `/etc/omnistat` with `omnistat.yaml`, and any drop-ins
  the admin created, and MUST say that it kept them and where they are.

### Runtime under systemd (extends 003 FR-026…FR-028)
- **FR-022** When omnistat's log output goes to the journal, each record MUST carry its
  journal priority: error → err, warn → warning, info → info, debug → debug. The text is
  the line a console run prints in the configured format. Records produced before the
  configuration is loaded (a config error, for example) MUST get priorities too. On a
  terminal, or anywhere else, output is unchanged.
- **FR-023** A stop from systemd (SIGTERM) is handled as 003 FR-020 specifies. The unit
  MUST give the final publish at least the HTTP timeout to complete before systemd
  forces the stop.

### Dry-run
- **FR-024** `service install --dry-run` and `service uninstall --dry-run` MUST run the
  same checks and print every change they would make. That includes:
  - paths, owners and modes;
  - the complete unit file;
  - the names of the stored settings;
  - whether the starter config would be created;
  - permission fixes;
  - the systemd actions (reload, enable, start or restart, stop, disable).

  They change nothing (constitution IV).

## Non-functional requirements
- **NFR-001** (security) After install, a user other than root can:
  - not read the token or any other stored setting (FR-013);
  - not change the installed binary, the unit or the config (FR-006, FR-007, FR-012);
  - not see the settings through `systemctl show` or `/proc`.

  The service runs with no capabilities as a dynamic user (FR-008). A non-root user
  account on the acceptance host verifies all of this.
- **NFR-002** (hardening) `systemd-analyze security omnistat` MUST rate the installed
  unit's overall exposure "OK" or better on the acceptance host. The value is recorded
  in the implementation notes.
- **NFR-003** (resources) Running as a service adds nothing at runtime: memory and
  network behave as 003 specifies, and omnistat writes nothing to disk.
- **NFR-004** (testability) Everything except the calls into systemd, file ownership and
  the terminal MUST be testable in `go test` on any OS, as in 006 NFR-003. That includes:
  - deciding what install and uninstall would do (the plan);
  - the unit's text;
  - the settings file's format and how it is read back;
  - choosing settings, and the prompts;
  - the starter config, which must stay valid against the config loader;
  - the priority mapping;
  - readiness.

  The OS calls sit behind a narrow seam that tests replace.
- **NFR-005** (acceptance, Linux) Proven in two steps:
  1. **By the agent, in disposable systemd containers** (Fedora 44, Debian 12,
     Ubuntu 24.04, Rocky 9 and Rocky 8) against the local development API and its test
     project. Each runs:
     - install with the token at the prompt and with the settings passed through;
     - identity and a publish read back from the project;
     - stop with a final publish, kill with a restart after a minute, and a revoked
       token;
     - upgrade by re-install, `--replace-token`, and a drop-in kept;
     - uninstall leaving only `/etc/omnistat`;
     - the NFR-001 checks as a non-root user;
     - `systemd-analyze security`.
  2. **By the owner, on their Fedora workstation**, following
     `specs/features/007-linux-service/acceptance.md` with `sudo`: install, reboot,
     journal, the checks as a standard user, upgrade, uninstall. The host is left as it
     was apart from `/etc/omnistat`.
- **NFR-006** (acceptance, Windows) The amendments to 006 (FR-014, FR-017) are verified
  on the owner's Windows VM in the next release candidate, together with the pending
  006 FR-020 check (constitution V).
- **NFR-007** (docs) The README MUST document Linux install, upgrade, token rotation,
  proxy, uninstall, logs and the file locations, with the same structure as the Windows
  section, and MUST show how to pass settings through `sudo`.

## Data & integration contract

Nothing new is read from or written to Omnismith. A Linux host installed as a service
resolves and publishes exactly as a console run does (002, 003). The feature adds no
templates and no attributes.

On the host, install creates or updates:

| Path | Owner, mode | Kept by uninstall |
|------|-------------|-------------------|
| `/usr/local/bin/omnistat` | root, 0755 | no |
| `/etc/systemd/system/omnistat.service` | root, 0644 | no |
| `/etc/omnistat/` | root, 0755 | yes |
| `/etc/omnistat/omnistat.yaml` (starter, only if absent) | root, 0644 | yes |
| `/etc/omnistat/omnistat.env` (stored settings) | root, 0600 | no |

On Windows, install additionally creates `%ProgramData%\omnistat\omnistat.yaml` when it
is absent (FR-014).

## Edge cases & failure modes

- **`sudo` drops the environment.** A token or project id exported in the caller's shell
  does not reach install unless passed through. Install asks for what is missing
  (FR-015, FR-017), and the README shows `--preserve-env`. `sudo -E` works where the
  sudoers policy allows it.
- **Network not online at boot** (for example, no `NetworkManager-wait-online` or
  `systemd-networkd-wait-online`): the first start may fail and is retried one minute
  later (FR-009). The retry goes to the journal.
- **Revoked token**: every start fails, once a minute, each time with an error in the
  journal. Nothing is written to the project.
- **Config file deleted after install**: the service fails to start, and the journal
  names the missing file. Re-running install restores the starter. (On Windows, a
  missing file means defaults, as 006 specifies.)
- **Config edited with an error** (for example, a typo or `access_token` in it): the
  service fails to start with the config error in the journal. A `schema plan` or
  `identity` run with `--config /etc/omnistat/omnistat.yaml` shows the same error before
  a restart.
- **The stored settings file edited by hand**: it is plain systemd environment-file
  syntax, and install reads it back on the next run. A line install cannot parse is an
  error that names the line number, never its content.
- **An existing `/usr/local/bin/omnistat`** placed by hand, with no unit: install
  replaces it and says so (dry-run shows "replace").
- **A foreign `omnistat` unit** (hand-written earlier, possibly from the README's old
  advice, or shipped by a package): install and uninstall refuse and name its path
  (FR-019).
- **SELinux enforcing** (Fedora, RHEL): files are created in their final directories,
  so they get those directories' default labels. The installed binary MUST NOT keep a
  home-directory label.
- **Older systemd (239–246)**: a few sandboxing options are unknown there. systemd logs
  a warning for each at reload and ignores it. The service works, and the exposure score
  may be higher. This is documented, and Rocky 8 acceptance records it.
- **systemd without a running manager** (a chroot or a container without init): FR-004.
- **Future modules that need more access** (for example, network interfaces for
  `ip-address`): the module's spec widens the sandbox. The next install rewrites the
  unit, so an upgrade ships the change.
- **Stop timeout shorter than the final publish** because `http.timeout` is set very
  high: FR-023 derives the stop timeout, so systemd waits for it.
- **API unreachable / 401 / 403 / 404 / 422 / 429 while running**: unchanged from
  001–003.

## Out of scope

- macOS (launchd). There is no use case and no hardware to accept it on.
- SysV init, OpenRC, runit, upstart, and systemd older than 239.
- A per-user (`systemctl --user`) install.
- `.deb`/`.rpm` packages and repositories. A package would own the unit and conflict
  with FR-019 by design. That is a later feature.
- Storing the token as an (encrypted) systemd credential. It was considered, see
  "Decisions".
- `start`, `stop` and `status` subcommands: `systemctl` does this.
- Several instances on one host, or custom unit names.
- A systemd watchdog, or status text in `systemctl status`.
- Persisting the buffer across restarts (003).

## Decisions taken during review (2026-09-25)

Chosen by the owner:
- **Install needs `sudo`; the service runs unprivileged.** The config is in
  `/etc/omnistat` and the unit is system-wide, so install needs root. The service itself
  needs no write access and runs as a dynamic user.
- **The token is in a root-only environment file** (`/etc/omnistat/omnistat.env`, 0600),
  not a systemd credential. This mirrors the Windows model (ADR-0011: "the same trust
  level as a root-only `EnvironmentFile`"). It needs no change to how omnistat reads
  settings, and it works on systemd 239, so RHEL-family 8 hosts are included.
  Credentials would keep the token out of the process environment. omnistat runs no
  child processes and its environment is readable only by its own user and root, so the
  gain does not pay for dropping RHEL 8 and adding a second store.
- **Install writes a commented starter config on both platforms** (FR-014), amending 006.
- **Acceptance** is done in disposable systemd containers by the agent, then by the
  owner on their workstation with `sudo` (NFR-005).

Proposed while drafting (for the owner to confirm):
- **DynamicUser, not a created `omnistat` user.** It is the Linux counterpart of
  `NT SERVICE\omnistat`: no account to create, clean up or give a password. It also
  brings systemd's sandbox defaults, such as a read-only filesystem and no privilege
  escalation.
- **`/usr/local/bin`** for the binary: the FHS location for software installed by the
  local admin. It is root-owned, and it is on the PATH, so `omnistat identity` works
  right after install.
- **Readiness notification** (FR-010). `systemctl start` then fails when the service
  cannot start, for example with a revoked token, so install and admins see it
  immediately. The 5-second heuristic stays only as a guard against a crash right after
  startup.
- **Journal priorities** (FR-022). They are the Linux counterpart of 006's Event Log
  severities.
- **Ask for a missing project id** (FR-017, both platforms). `sudo` strips the
  environment, and asking is friendlier than a failure with a long `--preserve-env`
  hint.
- **Lower-case proxy variables** (FR-013). Linux tools and admins commonly use them, and
  omnistat's HTTP client honours both spellings.
- **An explicit config path in the unit** (FR-007, FR-012). `systemctl cat omnistat`
  then shows exactly which file the service reads, and a deleted file is an error rather
  than silently falling back to defaults. The starter file (FR-014) makes this safe.
- **Drop-ins survive install and uninstall.** An admin's local changes are theirs.

## Open questions

None.

## Implementation notes (2026-09-25)

All tasks are done. `make all crosscheck` is green, and golangci-lint is clean for the
Windows build too.

**Owner acceptance, 2026-09-25.** The owner ran both runbooks and confirmed them as a
whole; no per-step table was recorded:
- `acceptance.md` on their Fedora workstation, with `sudo` (NFR-005/2);
- 006 runbook step 17 on their Windows VM (NFR-006): the project prompt, and the starter
  config created once with the directory's ACL and then kept. The same Windows run
  covers 006 FR-020.

ADR-0012 is accepted. The feature ships in 0.2.0.

**Container acceptance (NFR-005/1), 2026-09-25.** `make e2e-systemd` ran against the
local API and the "Omnistat Test" project, in privileged, host-isolated containers (see
the plan).

| Distro | systemd | Checks | `systemd-analyze security` |
|---|---|---|---|
| Fedora 44 | 259 | 46/46 | 1.1 OK |
| Debian 12 | 252 | 46/46 | 1.1 OK |
| Ubuntu 24.04 | 255 | 46/46 | 1.1 OK |
| Rocky Linux 9 | 252 | 46/46 | 1.1 OK |
| Rocky Linux 8 | 239 | 46/46 | 1.2 OK |

The checks cover:
- refusals: not root, and a foreign unit;
- the dry-run, and install with typed project id and token;
- files, modes and owners;
- the dynamic user, with no capabilities and a read-only system;
- publishing, and the entity read back;
- journal priorities 6, 3 and 7;
- another user denied the settings file, the process environment, `systemctl show`, the
  binary, the unit and the config;
- a clean stop with a final publish, and a restart one minute after a kill;
- a revoked token failing the start, retried, at `err`;
- an upgrade from another path, with merged settings, the config and a drop-in kept;
- `--replace-token`;
- a reboot (container restart) with the same entity;
- uninstall and its idempotence.

On systemd 239, `ProtectKernelLogs`, `ProtectClock`, `ProtectHostname` and `ProtectProc`
are unknown and ignored with a warning, as the edge case expected.

Deviations from the plan, and findings:

- **`make build` was not static.** Rocky 8's glibc refused the first local build: the
  Makefile built without `CGO_ENABLED=0`, unlike the release. It now builds CGO-free,
  per constitution V. Released binaries were never affected.
- **Uninstall order.** The settings file (with the token) and the binary are removed
  *before* the unit, not after. An interrupted uninstall then still finds the unit and
  finishes when run again, instead of saying "not installed" with the token file left
  behind.
- **systemd's quoting rules**, checked against systemd 259 with `systemd-run`. A quote
  opens only at the start of a value or right after a closing quote; quotes after any
  other character are literal. The parser follows this, and the test's forms are the
  checked ones.
- **Seam changes:**
  - `winsvc.Host` gained `FileExists` and `CreateFile` (for the starter) and
    `PromptLine`;
  - `service.Plan` gained `Unit`, `LogHint` and `ConfigPresent`. The CLI no longer
    `os.Stat`s the config path itself.
- **`sudo` and `/usr/local/bin`.** RHEL-family `secure_path` omits `/usr/local/bin`, so
  the README spells out `/usr/local/bin/omnistat` in `sudo` commands.
- **`ErrNotInteractive`** now reads "no terminal to prompt on", on both platforms.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md` (1.1.0)
