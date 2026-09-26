# omnistat

A modular [Omnismith](https://omnismith.io) exporter for hosts. Each module
(`machine-id`, `hostname`, `ip-address`, `cpu`, …) declares the templates and
attributes it needs, omnistat reconciles that schema in the target project, then
publishes dimensions and ingests metrics. Slugs have stable defaults and can be
remapped in config to fit an existing schema or marketplace blueprint.

> Status: **early.** Developed spec-first — see [`specs/README.md`](specs/README.md).
> Features 001 (schema reconciliation), 002 (host identity), 003 (run loop, publisher,
> `hostname` module), 004 (`cpu`, the first metric provider), 005 (`memory`), 006
> (Windows service), 007 (systemd service) and 008 (`disk`) are implemented; the next
> specs add further value modules (`ip-address`, `net`, …).

## Why

Omnismith is a dynamic data platform (templates → attributes → entities, with native
metric time series, dashboards and automations). `omnistat` is the first app in the
`omnismith-apps` organisation and doubles as a dogfooding exercise for the official
Go SDK, [`github.com/omnismith-sdk/go`](https://github.com/omnismith-sdk/go).

## Install

Download the archive for your OS and CPU from
[Releases](https://github.com/omnismith-apps/omnistat/releases) and check it against
`checksums.txt`. Each archive holds one static binary (`omnistat`, or `omnistat.exe` on
Windows), with `README.md`, `CHANGELOG.md` and `.env.example`.

| OS | amd64 (x86-64) | arm64 |
|----|----------------|-------|
| Linux | `omnistat_<ver>_linux_amd64.tar.gz` | `omnistat_<ver>_linux_arm64.tar.gz` |
| macOS | `omnistat_<ver>_darwin_amd64.tar.gz` (Intel) | `omnistat_<ver>_darwin_arm64.tar.gz` (Apple silicon) |
| Windows | `omnistat_<ver>_windows_amd64.zip` | `omnistat_<ver>_windows_arm64.zip` |

The binaries are not code-signed yet. On macOS, clear the quarantine flag once with
`xattr -d com.apple.quarantine ./omnistat`. On Windows, SmartScreen may warn about
an unknown publisher the first time the binary runs.

To build from source instead, run `make build` (Go 1.26).

## Quick start

```bash
make build
export OMNISMITH_ACCESS_TOKEN=omni_...      # never put this in a file that is committed
export OMNISMITH_PROJECT_ID=<project uuid>
./bin/omnistat schema plan                  # dry-run: what would be created (exit 2 = changes pending)
./bin/omnistat schema apply                 # create what is missing — additive only, never deletes
./bin/omnistat schema verify                # for hosts whose token cannot write the schema
./bin/omnistat identity                     # this host's identity, its source, and the entity it maps to
./bin/omnistat run --dry-run                # what would be published, without writing (add --json for a script)
./bin/omnistat run                          # reconcile → resolve the host entity → collect every module → publish, once
./bin/omnistat run --daemon                 # keep going: modules on their own cadence, one publish per interval
```

`run` exits 0 when everything was published, 2 when some module failed to collect
(the rest was still published), 1 on error. The daemon publishes right after the first
collection, then every `publish.interval`; on SIGTERM/SIGINT it publishes what is
buffered and exits 0. Observations are stamped when collected, so a network outage only
delays them (the buffer holds up to 5 000 observations per metric).

### Linux service (systemd)

On Linux with systemd (version 239 or later: RHEL/Rocky/Alma 8+, Debian 11+,
Ubuntu 20.04+, Fedora), omnistat installs itself as a hardened systemd service that
starts at boot once the network is online, restarts after a failure and logs to the
journal (spec 007). Unpack the archive anywhere and run:

```bash
sudo ./omnistat service install --dry-run   # what would change, the full unit included; changes nothing
sudo ./omnistat service install             # asks for the project id and the token (no echo)
```

`sudo` drops your environment, so install asks for what it does not find. To script it,
pass the settings through instead:

```bash
sudo --preserve-env=OMNISMITH_ACCESS_TOKEN,OMNISMITH_PROJECT_ID ./omnistat service install
```

Install checks the token, the project and the host identity before changing anything.
It then does the following:

| Path | What | Owner, mode |
|------|------|-------------|
| `/usr/local/bin/omnistat` | the binary, copied from where you ran install | root, 0755 |
| `/etc/omnistat/omnistat.yaml` | a commented starter config (every default in force); only if absent, never overwritten | root, 0644 |
| `/etc/omnistat/omnistat.env` | the token, project id and any `OMNISMITH_BASE_URL`, `OMNISTAT_IDENTITY` and proxy variables (either case) from your environment | root, **0600** |
| `/etc/systemd/system/omnistat.service` | the unit, enabled and started | root, 0644 |

The service runs as an unprivileged user that systemd allocates while it runs
(`DynamicUser=`), with no capabilities, a read-only view of the system and systemd's
sandbox (`systemd-analyze security omnistat` rates it about 1.1, "OK"). Only root can
read the token or change the binary, the unit or the config. It tells systemd when it is
ready, so a start with a bad token fails visibly.

| Task | How |
|------|-----|
| Configure | edit `/etc/omnistat/omnistat.yaml`, check it with `omnistat --config /etc/omnistat/omnistat.yaml schema plan`, then `sudo systemctl restart omnistat` |
| Upgrade | run `sudo ./omnistat service install` from the new version: binary and unit are replaced, settings and config are kept |
| Rotate the token | `sudo /usr/local/bin/omnistat service install --replace-token` |
| Change the proxy | `sudo HTTPS_PROXY=http://proxy:3128 /usr/local/bin/omnistat service install` (other settings are kept) |
| Local unit changes | `sudo systemctl edit omnistat` — install rewrites the unit but never touches drop-ins |
| Start / stop / status | `systemctl start`, `stop` (publishes what is buffered first), `status omnistat` |
| Logs | `journalctl -u omnistat` (problems only: `-p warning`): the first publish, then a `publish summary` every 15 minutes, plus any failure as it happens |
| Remove | `sudo /usr/local/bin/omnistat service uninstall` (keeps `/etc/omnistat` and your drop-ins; nothing is changed in the Omnismith project) |

If you wrote an `omnistat.service` by hand before, install refuses to touch it: stop,
disable and delete it (`systemctl disable --now omnistat`, remove the file,
`systemctl daemon-reload`), then install. Hosts without systemd (Alpine/OpenRC,
containers) run `omnistat run --daemon` under their own supervisor. macOS has no
service command.

The commands above spell out `/usr/local/bin/omnistat` because `sudo` on RHEL-family
systems does not search `/usr/local/bin`.

### Windows service

On Windows, omnistat installs itself as a service that starts at boot, restarts after a
failure and logs to Event Viewer (spec 006). Unzip the archive anywhere and, in an
**elevated** PowerShell (Run as administrator):

```powershell
$env:OMNISMITH_PROJECT_ID = "<project uuid>"
.\omnistat.exe identity                         # optional: check the identity first
.\omnistat.exe service install --dry-run        # what would change; changes nothing
.\omnistat.exe service install                  # asks for the token without echoing it (and the project id if unset)
```

Install checks the token, the project and the host identity before changing anything.
It then does the following:

- copies itself to `C:\Program Files\omnistat\omnistat.exe`;
- registers the service `omnistat` (automatic, delayed start, account
  `NT SERVICE\omnistat`, restarted one minute after any failure);
- stores the token, project id and any `OMNISMITH_BASE_URL`, `OMNISTAT_IDENTITY`,
  `HTTPS_PROXY`, `HTTP_PROXY` or `NO_PROXY` from your environment as the service's own
  environment, readable only by Administrators and SYSTEM;
- creates `C:\ProgramData\omnistat\omnistat.yaml`, a commented starter config, if there
  is none (an existing one is never changed);
- starts the service.

Type the token at the prompt rather than setting `$env:OMNISMITH_ACCESS_TOKEN`: PowerShell
saves typed commands to its history file. For a scripted install the variable works too.

| Task | How |
|------|-----|
| Configure | `C:\ProgramData\omnistat\omnistat.yaml` (only administrators can edit it), then restart the service |
| Upgrade | run `service install` from the new version: the binary is replaced, settings are kept |
| Rotate the token | `service install --replace-token` |
| Change the proxy | set `$env:HTTPS_PROXY`, then `service install` |
| Start / stop | Services, `Start-Service omnistat`, `Stop-Service omnistat` (a stop publishes what is buffered first) |
| Logs | Event Viewer → Windows Logs → Application, source `omnistat`: the first publish, then a `publish summary` every 15 minutes (`log.summary_interval`), plus any failure as it happens |
| Remove | `service uninstall` (the config directory is kept; nothing is changed in the Omnismith project) |

The binary is not code-signed yet, so SmartScreen may warn when you first run the
downloaded `omnistat.exe`. On Windows, `load_avg_*` is not collected: Windows has no
load average.

### Modules

| Module | Publishes | Default cadence |
|--------|-----------|-----------------|
| `machine-id` | `machine_id` (text) — the host entity's idempotency key; always enabled | once, at startup |
| `hostname` | `hostname` (text) — the entity's human-readable label | 5m |
| `cpu` | `cpu_usage_pct`, `load_avg_1`, `load_avg_5`, `load_avg_15` (metrics); `cpu_model`, `cpu_cores`, `cpu_arch` (dimensions) | 10s |
| `memory` | `mem_used_pct`, `mem_available_mib` (metrics); `mem_total_mib` (dimension) | 30s |
| `disk` | `disk_root_used_pct`, `disk_root_available_gib`, `disk_root_inodes_used_pct`, `disk_read_mibps`, `disk_write_mibps`, `disk_read_iops`, `disk_write_iops`, `disk_busy_pct` (metrics); `disk_root_total_gib` (dimension) | 30s |

CPU usage is the non-idle share of the CPU time that elapsed since the previous reading,
aggregated across every logical CPU, so a fully busy 8-core host reports 100, not 800,
published with two decimal places.
The first collection of a process measures over a short 250ms window so that a one-shot
`omnistat run` publishes a real number; every later one spans the whole interval.

Memory "available" is the operating system's own estimate of memory that can be given to
programs without swapping (Linux `MemAvailable`, Windows available physical memory), not
its literal "free" figure, which leaves reclaimable cache out and makes a healthy host look
full. `mem_used_pct` is (total − available) ÷ total, so it rises as a host approaches swap
and compares hosts of any size (two decimal places); amounts are whole MiB, rounded down.

Disk space is reported for the **system volume**: `/` on Linux, the drive holding
Windows on Windows (not assumed to be `C:`), and the startup disk's data volume on macOS,
where `/` is a sealed snapshot. `disk_root_used_pct` is used ÷ (used + available), the
same as `df`, so space reserved for root counts as neither. Amounts are GiB, rounded down
to two decimals. Disk I/O is counted once, on the **physical disks**: a write through LVM
on a partition shows up on three devices in `/proc/diskstats`, and omnistat counts only
the disk. Windows keeps the counters per lettered volume, and those are summed.
Throughput is in MiB/s and operations per second over the collection interval.
`disk_busy_pct` is the busiest disk's share of time with I/O in flight: one saturated
disk is not averaged away. As for CPU usage, a one-shot run measures the rates over a
short first window. On a filesystem with no inode limit (btrfs), omnistat logs that once
and publishes no inode figure.

An attribute may declare the platforms it can be collected on. **Load averages are not
collected on Windows**, which maintains no load average — omnistat says so once at
startup and publishes nothing for them rather than substituting the nearest available
number. The attributes are still declared in the project schema everywhere, so a mixed
fleet converges on one schema. Likewise **macOS publishes only `mem_total_mib`**: it
maintains no estimate of memory available without swapping, and a figure computed from its
page counts would overstate the headroom. **`disk_busy_pct` and `disk_root_inodes_used_pct` are
Linux-only**: macOS keeps no busy-time counter, the Windows idle-time counter is not
among the readings omnistat takes, NTFS has no inode limit, and APFS creates inodes on
demand.

The host identity is derived from the OS machine id (`/etc/machine-id` on Linux,
the platform UUID on macOS) as a keyed hash — the raw id is never published. Pin it
for clones or containers with `OMNISTAT_IDENTITY=…` or `identity.static` in the config.

Optional `omnistat.yaml` (picked up from the working directory, or `--config`; the
services read `/etc/omnistat/omnistat.yaml` and `C:\ProgramData\omnistat\omnistat.yaml`,
where install leaves a commented starter):

```yaml
project_id: 01a0c47a-...
schema:
  mode: apply            # apply | verify | off
  host_template: server  # remap the default `host` template to an existing one
publish:
  interval: 60s          # how often the daemon publishes (1s–1h)
modules:
  hostname:
    interval: 5m         # per-module collection cadence (1s–24h; default from the module)
  cpu:
    interval: 10s        # cpu's own default
    attributes:
      usage: { slug: cpu_usage }   # fit an existing attribute
  memory:
    interval: 30s        # memory's own default
    # enabled: false     # switch a module off: neither its schema nor its values
  disk:
    interval: 30s        # disk's own default; the I/O rates are averages over it
identity:
  static: rack7-node3    # optional: pin the identity instead of autodiscovering it
http: { timeout: 15s, retries: 3 }
log:
  level: info            # debug | info | warn | error
  format: text           # text | json
  summary_interval: 15m  # daemon: one "publish summary" line per period; 0 = log every publish
```

In daemon mode, a successful publish is logged at `debug`. At `info` you see the first
publish, a recovery after failures, and a `publish summary` every
`log.summary_interval`. Failures and dropped observations are logged when they happen.
A one-shot `omnistat run` logs its publish as before.

Each module ships stable default slugs; overrides exist to fit an existing schema.
Reconciliation only ever *creates* templates, attributes, list options and bindings.
An existing attribute whose kind differs from the manifest is reported as a conflict
and nothing is written.

## Layout

| Path | What |
|------|------|
| `specs/` | Constitution, feature specs, plans, tasks, ADRs — the source of truth |
| `docs/reference/` | Facts about the Omnismith API and Go SDK |
| `cmd/omnistat/` | CLI entry point |
| `internal/` | Application packages (created per plan) |
| `AGENTS.md` | Operating manual for coding agents |

## Contributing

1. Draft or pick a spec in `specs/features/`.
2. Plan → tasks → implement, following `AGENTS.md`.
3. `make all` must pass; CI runs the same gate plus `scripts/check-specs.sh`.

## Releasing

1. In `CHANGELOG.md`, move the *Unreleased* entries under a new `## [X.Y.Z] - YYYY-MM-DD`
   heading.
2. Commit, then tag and push: `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. `.github/workflows/release.yml` runs the tests, builds every target with
   [GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`), and publishes a GitHub
   Release with the archives, `checksums.txt`, and that changelog section as its notes.
   The job fails if the tag has no changelog section. A tag with a suffix
   (`v0.2.0-rc.1`) is published as a pre-release, and uses the *Unreleased* section
   when it has none of its own, so a release candidate needs no changelog edit.

`make release-snapshot` builds the same archives into `dist/` without publishing
anything, and `make release-check` validates the config.

## License

TBD.
