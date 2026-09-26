---
feature: 009-upgrade
status: approved         # draft | review | approved | implemented | superseded
approved: 2026-09-26
created: 2026-09-26
owners: [evgenii]
supersedes: null
adr: [0011, 0012, 0013]
depends_on: [006-windows, 007-linux-service]
---

# Feature: `omnistat upgrade` — update the installed service from the releases

## Summary

Since 006 and 007, a host runs omnistat as a service installed with
`omnistat service install`. To upgrade it, the operator:
1. opens the GitHub releases page;
2. picks the archive for the host's OS and CPU, and checks it against `checksums.txt`;
3. copies it to the host and unpacks it;
4. runs `service install` from the new binary.

That is four manual steps per host, repeated on every host of a fleet, and the checksum
step is the one that gets skipped.

This feature adds one command, `omnistat upgrade`. It finds the latest release (or the
one asked for), downloads the archive for this host, verifies it, and hands over to the
new binary's own `service install`. The result is exactly what the manual flow gives,
because the new version does the install:
- the new unit or service registration;
- the stored settings and config kept;
- the same checks before any change;
- the same report.

`--check` only reports whether an upgrade is available, with an exit status a script can
use. `--dry-run` shows what the new version's install would change. Nothing about the
running service changes until the downloaded binary has been verified.

## Users & context

- **Fleet operator** with tens to hundreds of Linux and Windows hosts, driving them with
  SSH loops, Ansible, or a remote-management tool. Wants a single command per host that
  is safe to run twice, exits non-zero when something is wrong, and does not need a
  file copied to the host first.
- **Admin at a client** with one or two hosts, following the README. Wants
  `sudo omnistat upgrade` to "just work", including behind the proxy the service
  already uses.
- **Where it runs**: on a host where `omnistat service install` has installed the
  service. It needs outbound HTTPS to GitHub (or to a mirror, FR-015), directly or
  through a proxy. It needs the same rights as `service install`: root on Linux, an
  elevated prompt on Windows.
- **Platforms**:
  - **Linux with systemd**: supported, the primary target. amd64 is accepted, arm64 is
    built.
  - **Windows**: supported. amd64 is accepted, arm64 is built (constitution V).
  - **macOS** and **Linux without systemd**: there is no installed service to upgrade.
    `upgrade` says so, as `service` does (FR-004).

## User stories

### US-1 — Upgrade a host with one command (P1)
As a fleet operator, I want `sudo omnistat upgrade` to bring the installed service to the
latest release, so that I do not download, verify and unpack archives by hand.

**Acceptance scenarios**
1. **Given** a host running the service at v0.3.0 and a published v0.4.0, **When** I run
   `sudo omnistat upgrade`, **Then** the output shows the installed and the target
   version. The v0.4.0 archive for this host's OS and CPU is downloaded and verified
   against the release's checksums. The new binary's `service install` then runs and
   prints its usual report. Afterwards the service runs v0.4.0, with the same stored
   settings, config file and host entity, and the exit status is 0.
2. **Given** the service already runs the latest release, **When** I run upgrade, **Then**
   it says the service is up to date, changes nothing, downloads no archive, and exits 0.
3. **Given** a download that does not match the checksums, or a binary that does not
   report the expected version, **When** I run upgrade, **Then** nothing about the
   service changes. The message names the check that failed, and the exit status is 1.
4. **Given** a step of the new version's install fails (the service does not start, for
   example), **Then** upgrade exits with that failure's status. The output is the
   install's own, so the operator reads the same message the manual flow gives.

### US-2 — Know which hosts are behind (P1)
As a fleet operator, I want to ask a host whether an upgrade is available, without
changing anything, so that I can plan a rollout.

**Acceptance scenarios**
1. **Given** an installed service at v0.3.0 and a published v0.4.0, **When** I run
   `sudo omnistat upgrade --check`, **Then** it prints the installed version, the latest
   version, and that an upgrade is available. It exits 2, downloads no archive, and
   changes nothing.
2. **Given** the service runs the latest release, **When** I run `--check`, **Then** it
   says so and exits 0.
3. **Given** the releases cannot be reached, **When** I run `--check`, **Then** it says why
   and exits 1.

### US-3 — See what the upgrade would change (P1)
As an admin, I want a dry-run of the upgrade, as for every other mutation (constitution
IV).

**Acceptance scenarios**
1. **Given** an available upgrade, **When** I run `sudo omnistat upgrade --dry-run`,
   **Then** it downloads and verifies the new release. It then prints what the new
   version's `service install --dry-run` would do, the full unit file included on Linux.
   The service, its binary, its settings and its config are unchanged, and the downloaded
   files are removed.

### US-4 — Pin a version or roll back (P2)
As a fleet operator, I want to install a specific release, older or newer, so that I can
roll out a release candidate to a canary host, or go back to the previous version when a
release misbehaves.

**Acceptance scenarios**
1. **Given** an installed v0.4.0, **When** I run `sudo omnistat upgrade --version v0.3.0`,
   **Then** v0.3.0 is downloaded, verified and installed, and the output says that this
   is a downgrade.
2. **Given** a published pre-release `v0.5.0-rc.1`, **When** I run
   `upgrade --version v0.5.0-rc.1`, **Then** it is installed. Without `--version`, a
   pre-release is never chosen.
3. **Given** a version that does not exist, or has no archive for this host, **When** I
   run `upgrade --version …`, **Then** it says so, changes nothing, and exits 1.

### US-5 — Upgrade behind a proxy (P2)
As an admin whose hosts reach the internet only through a proxy, I want upgrade to use
the proxy the service already uses, so that I do not pass it again on every host.

**Acceptance scenarios**
1. **Given** a service installed with `HTTPS_PROXY` stored in its settings, and an
   upgrade run with `sudo` (which drops the caller's environment), **When** I run
   upgrade, **Then** the download goes through the stored proxy.
2. **Given** a proxy set in the environment of upgrade, **Then** that one is used instead.
   The new version's install then stores it, as install does with any setting from its
   environment (007 FR-013).

### US-6 — Upgrade from a mirror (P3)
As an operator of hosts that cannot reach GitHub, I want to point upgrade at an internal
mirror of the releases.

**Acceptance scenarios**
1. **Given** `OMNISTAT_RELEASES_URL=https://mirror.example/omnistat` whose layout matches
   GitHub's releases, **When** I run upgrade, **Then** the releases are read from the
   mirror and verified in exactly the same way.
2. **Given** an `http://` mirror URL on a host other than the local machine, **When** I
   run upgrade, **Then** it refuses to use it and exits 1.

## Functional requirements

### Command and preconditions
- **FR-001** omnistat MUST provide `omnistat upgrade [--check] [--dry-run]
  [--version vX.Y.Z]`, on the platforms where `service install` works (006, 007).
  `--check` and `--dry-run` cannot be combined.
- **FR-002** Every form of upgrade, `--check` and `--dry-run` included, MUST require the
  rights `service install` requires: root on Linux, an elevated prompt on Windows. It
  MUST refuse with the same message, before any network access. *(Upgrade reads the
  service's stored settings for the proxy, FR-012, and those are readable only with
  these rights.)*
- **FR-003** Upgrade MUST require a service installed by omnistat. With none, it changes
  nothing and says that upgrade updates the installed service and that
  `omnistat service install` installs one. The exit status is 1. With a service omnistat
  did not install (007 FR-019, 006 FR-019), it refuses as `service install` does.
- **FR-004** Where the service commands are not supported (macOS; Linux without systemd),
  upgrade MUST fail as `service` does (006 FR-027, 007 FR-003, 007 FR-004).

### Choosing the release
- **FR-005** The **installed version** is what the installed service's binary reports
  (`omnistat version`). When it cannot be run, or is not a release version (a
  development build), the installed version is *unknown*. Upgrade says so and treats any
  release as newer.
- **FR-006** Without `--version`, the **target** is the latest published release,
  excluding pre-releases and drafts. With `--version`, the target is exactly that
  release, a pre-release included. The leading `v` is optional.
- **FR-007** Upgrade MUST compare versions by semantic versioning:
  - installed equals target: "up to date", nothing downloaded, exit 0;
  - installed newer than the latest release, without `--version`: it says so, nothing
    changes, exit 0;
  - target older than installed, which happens only with `--version`: a downgrade, and
    the output says so.
- **FR-008** The archive MUST be the one the release publishes for this host's OS and
  CPU, named as the release pipeline names it. A release without one fails with a
  message naming the OS and CPU.

### Download and verification
- **FR-009** Before any change, upgrade MUST:
  - download the release's `checksums.txt` and the archive;
  - verify the archive's SHA-256 against its line in `checksums.txt`;
  - unpack only the `omnistat` binary (`omnistat.exe` on Windows) from the archive;
  - run it with `version` and check that it reports the target version.

  Any failure fails the upgrade, with a message that names the check. The service and
  every installed file are left as they were.
- **FR-010** Upgrade MUST use HTTPS, and MUST refuse a redirect from HTTPS to plain HTTP.
  The only exception is a mirror on the local machine (FR-015). Downloads MUST have
  deadlines and size limits, so a stalled or oversized download fails instead of
  hanging or filling the disk.
- **FR-011** The downloaded files MUST be unpacked into a directory that only root (or
  Administrators) can write, on a filesystem that allows running programs. That means
  the installed binary's own directory, not the system temporary directory (hardened
  hosts mount `/tmp` with `noexec`). The directory MUST be removed when upgrade ends,
  whatever the outcome. Leftovers of an interrupted upgrade MUST be removed by the next
  one.
- **FR-012** Downloads MUST use the proxy settings from upgrade's own environment,
  `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` (either case on Linux). When the environment
  sets none of them, downloads use the ones stored for the service (006 FR-013,
  007 FR-013).

### Handing over
- **FR-013** After verification, upgrade MUST run the downloaded binary's
  `service install` (with `--dry-run` for a dry-run) and attach its input and output to
  the terminal. That install sees upgrade's environment unchanged. Its exit status
  becomes upgrade's. The new version therefore does its own checks, writes its own unit
  or registration, keeps the stored settings and the config, and restarts the service.
  An interrupt (Ctrl-C) reaches that install, which handles it. Upgrade does not kill it
  halfway.
- **FR-013a** On Windows, when upgrade runs from the installed binary, it MUST free that
  path before the hand-over and MUST put the previous binary back if the install leaves
  none there (see "Edge cases").
- **FR-014** After a successful handover, upgrade MUST end with one line naming the
  previous and the new version. After a failed one, it says that the upgrade did not
  complete and points to the install's output above, which names the failed step.

### Mirror
- **FR-015** The releases location defaults to
  `https://github.com/omnismith-apps/omnistat/releases`. `OMNISTAT_RELEASES_URL`
  replaces it with a mirror that has the same layout:
  - `<url>/latest/download/checksums.txt` for the latest release;
  - `<url>/download/<tag>/<file>` for each release file.

  The URL MUST use `https://`. `http://` is accepted only for a loopback host
  (`localhost`, `127.0.0.1`, `::1`), which serves tests and a local mirror.

### Output and secrets
- **FR-016** Output MUST name the installed binary, the installed and target versions,
  the archive URL, and the result of each verification. It MUST NOT contain the value of
  any stored setting. A proxy URL can carry a password, so the proxy is named only by
  its variable.
- **FR-017** `--check` MUST exit 0 when the installed version is the target or newer, 2
  when an upgrade is available (the "attention needed" status of `schema plan` and
  `run`), and 1 on error.

## Non-functional requirements
- **NFR-001** (security) The binary that replaces the service's binary MUST be verified
  (FR-009) and MUST be run only from a location no unprivileged user can write to
  (FR-011), between the check and the use. Checksums over HTTPS protect against a
  corrupted or truncated download and a tampered mirror or proxy. They do not protect
  against a compromised GitHub release. ADR-0013 records this trust model and the
  signature verification it defers.
- **NFR-002** (reliability) An upgrade that fails before the handover leaves the host
  exactly as it was. Once it hands over, the failure behaviour is `service install`'s
  (006, 007).
- **NFR-003** (idempotence) Running upgrade again right after a successful one reports
  "up to date" and changes nothing (constitution IV).
- **NFR-004** (fleet-friendliness) No prompt appears when the service's settings are
  already stored, which is always the case after an install. Upgrade can therefore run
  unattended with `sudo -n`, Ansible `become`, or a remote PowerShell session.
- **NFR-005** (testability) Everything except the real network, the real service manager
  and running real binaries MUST be testable in `go test` on any OS. That includes:
  release resolution; choosing the asset; checksum and archive handling (tar.gz and zip);
  version comparison; proxy selection; URL policy; the staging directory; the handover
  arguments; and every exit status. Release downloads are faked with `httptest`.
- **NFR-006** (acceptance) Proven on Linux in a disposable systemd container, by
  upgrading from an installed release to a locally built one served by a loopback
  mirror. The owner then accepts it on Linux and on their Windows VM, with a published
  release candidate, including an upgrade run from the installed copy on Windows
  (FR-013a).
- **NFR-007** (docs) The README MUST replace its manual "Upgrade" rows with `upgrade`,
  keep the manual flow as the fallback, and document `--check`, `--version`, the proxy
  and the mirror.

## Data & integration contract

Nothing is read from or written to Omnismith by upgrade itself. The handed-over
`service install` runs its read-only pre-check against the project (006 FR-006,
007 FR-005), as it does when run by hand. No template or attribute is added.

Read from the releases location (GitHub by default):

| File | Used for |
|------|----------|
| `latest/download/checksums.txt` | the latest release: its version, from the archive names, and the expected hashes |
| `download/<tag>/checksums.txt` | the same for `--version` |
| `download/<tag>/omnistat_<ver>_<os>_<arch>.tar.gz` (`.zip` on Windows) | the archive |

On the host, upgrade itself only creates and removes a temporary
`.omnistat-upgrade-*` directory next to the installed binary. Everything else is
`service install`'s contract (006, 007).

## Edge cases & failure modes

- **GitHub unreachable, rate-limited or 404**: fails before any change, naming the URL
  and the HTTP status. The web download URLs are not the rate-limited REST API, so a
  fleet behind one address can upgrade together.
- **Release published while a fleet upgrades**: every host resolves the latest release
  when it runs. A rollout that must be uniform uses `--version`.
- **The operator's copy is older than the installed service** (for example
  `./omnistat upgrade` from an old download): the installed version, not the running
  binary's, is compared (FR-005). The newer release's install does the work.
- **An installed version older than 0.4.0** (no `upgrade` command): any 0.4.0+ binary
  runs `upgrade` against it. The first hop can be `./omnistat upgrade` from a downloaded
  0.4.0, or the manual flow.
- **`--version` older than the service commands** (0.1.x on Linux): the handed-over
  binary's `service` fails as it would by hand, before changing anything.
- **Windows: upgrade run from the installed `omnistat.exe`**: that file is in use while
  upgrade waits for the install, and Windows cannot overwrite a running program. Right
  before the hand-over, upgrade moves its own running binary into its staging directory,
  as uninstall does (006 FR-020), so the install finds the path free. This works for any
  target version, including releases older than this feature. If the install leaves no
  binary there, upgrade puts the previous one back, so the service never loses its
  program. The moved file is removed at the next restart. A dry-run moves nothing.
- **Interrupted download or unpack** (Ctrl-C, reboot, full disk): the service is
  untouched. A leftover directory is removed by the next upgrade (FR-011).
- **Interrupted install after the handover**: as for `service install` by hand. Running
  `upgrade` again completes it, because the installed version is still the old one.
- **Architecture**: a 64-bit Arm host running the amd64 build under emulation (Windows on
  Arm) gets the amd64 build again, because the target is the CPU the running binary was
  built for. `--version` cannot change that. The manual flow can.
- **Two upgrades at once on one host**: not supported, as with two installs at once. The
  second may remove the first's staging directory, and then the first fails its handover
  with nothing changed.
- **A mirror serving a different binary with matching checksums**: indistinguishable
  from a genuine release by design (NFR-001). A mirror is as trusted as GitHub.

## Out of scope

- **Signature verification** (cosign, minisign, GitHub attestations). It needs signing
  in the release pipeline and a key policy first. See ADR-0013.
- **Automatic or scheduled upgrades** by the service itself. The service runs
  unprivileged with a read-only filesystem (ADR-0011, ADR-0012) and cannot replace
  itself. An operator or a fleet tool decides when.
- **Upgrading a binary that is not installed as a service**, such as `run --daemon`
  under another supervisor, or a copy in a home directory. The manual download remains.
- **A rollback of a failed install** to the previous binary. The operator runs
  `upgrade --version <previous>`.
- Release channels beyond "latest stable" and "exactly this version".
- macOS.

## Decisions (confirmed by the owner, 2026-09-26)

The spec, plan, tasks and implementation were written in one pass, without
intermediate approval. The owner then confirmed these choices, and ADR-0013:

- **Hand over to the new binary's `service install`** rather than installing the
  download with the running binary's install logic. The new version's unit, sandbox
  and registration then ship with it, and the result equals the documented manual flow.
- **The installed version is compared, not the running binary's** (FR-005). It is what
  the operator asks about, and an old copy on disk cannot trigger a pointless re-install.
- **Web download URLs, not the GitHub REST API**: the version comes from the archive
  names in `latest/download/checksums.txt`. That avoids the API's rate limit of 60
  requests per hour per address, and a mirror is a plain static file tree.
- **Every form needs root or elevation**, `--check` included (FR-002). One rule, and it
  lets `--check` use the service's stored proxy.
- **Stage next to the installed binary** (FR-011), because CIS-hardened hosts mount
  `/tmp` with `noexec`.
- **Never downgrade without `--version`**, and never pick a pre-release without it.
- **`--check` exits 2 when an upgrade is available** (FR-017), following the repository's
  "attention needed" convention.

## Open questions

None.

## Implementation notes (2026-09-26)

T001–T050 are done. `make all crosscheck` is green, including golangci-lint for the
Windows build. What is left: owner acceptance (T051, which needs a published release
candidate) and the sync that follows it (T052). ADR-0013 is
accepted.

**Container acceptance (NFR-006), 2026-09-26.** `make e2e-upgrade` used the local API and
the "Omnistat Test" project. Each disposable systemd container installs the published
v0.3.0, then upgrades it from a loopback mirror (`scripts/e2e-mirror`) serving local
builds `v0.99.0` and `v0.98.0`, the latter with a tampered checksum. It rolls back to
v0.3.0 from the real GitHub releases. `/tmp` is `noexec` in these containers (Docker
`--tmpfs`).

| Distro | systemd | Checks |
|---|---|---|
| Fedora 44 | 259 | 38/38 |
| Debian 12 | 252 | 38/38 |
| Rocky Linux 8 | 239 | 38/38 |

The checks cover:
- refusal without root;
- `--check` exiting 2, then 0;
- a dry-run that changed nothing and left no staging directory;
- the upgrade: verified, the new install ran, the stored settings and config
  byte-identical, the same entity;
- idempotence;
- GitHub `--check` reporting a newer installed build;
- a tampered checksum refused with nothing changed;
- a non-loopback HTTP mirror refused;
- a rollback run from the installed copy, downloading from GitHub;
- no leftovers.

`make e2e-systemd` (007) still passes 48/48 on Fedora 44 after its harness became
sourceable.

Deviations from the plan, and findings:
- **The Windows fix moved from install to upgrade (FR-013a).** The first
  implementation taught `CopyBinary` to replace an in-use binary. That would only help
  targets containing the fix. A rollback to 0.3.0 run from the installed copy would have
  stopped the service and then failed to replace its binary. Upgrade now moves its own
  running binary into the staging directory before the hand-over, and puts it back if
  the install leaves none. `CopyBinary` and 006 are unchanged.
- **The Debian and Ubuntu test images lacked a CA bundle.** HTTPS to GitHub failed with
  "certificate signed by unknown authority", which is the expected, clear error. The
  images now install `ca-certificates`. Real hosts have it.
- **`scripts/e2e-systemd.sh` is sourceable.** The container boot is a function (`boot`),
  and `summary` prints the result. `scripts/e2e-upgrade.sh` reuses them.
- **A new dependency, `golang.org/x/net v0.50.0`** (`http/httpproxy` only). It is the
  release matching `x/sys v0.41.0`, so `x/sys` did not move.
- **Seams**: `App.Runner` and `App.StageDir` (tests), and `installer.Installation` on
  both backends.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md` (1.1.0)
