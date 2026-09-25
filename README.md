# omnistat

A modular [Omnismith](https://omnismith.io) exporter for hosts. Each module
(`machine-id`, `hostname`, `ip-address`, `cpu`, …) declares the templates and
attributes it needs, omnistat reconciles that schema in the target project, then
publishes dimensions and ingests metrics. Slugs have stable defaults and can be
remapped in config to fit an existing schema or marketplace blueprint.

> Status: **early.** Developed spec-first — see [`specs/README.md`](specs/README.md).
> Features 001 (schema reconciliation), 002 (host identity), 003 (run loop, publisher,
> `hostname` module), 004 (`cpu`, the first metric provider) and 005 (`memory`) are
> implemented; the next specs add further value modules (`ip-address`, `disk`, …).

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

### Windows service

On Windows, omnistat installs itself as a service that starts at boot, restarts after a
failure and logs to Event Viewer (spec 006). Unzip the archive anywhere and, in an
**elevated** PowerShell (Run as administrator):

```powershell
$env:OMNISMITH_PROJECT_ID = "<project uuid>"
.\omnistat.exe identity                         # optional: check the identity first
.\omnistat.exe service install --dry-run        # what would change; changes nothing
.\omnistat.exe service install                  # asks for the token without echoing it
```

Install checks the token, the project and the host identity before changing anything.
It then does the following:

- copies itself to `C:\Program Files\omnistat\omnistat.exe`;
- registers the service `omnistat` (automatic, delayed start, account
  `NT SERVICE\omnistat`, restarted one minute after any failure);
- stores the token, project id and any `OMNISMITH_BASE_URL`, `OMNISTAT_IDENTITY`,
  `HTTPS_PROXY`, `HTTP_PROXY` or `NO_PROXY` from your environment as the service's own
  environment, readable only by Administrators and SYSTEM;
- starts the service.

Type the token at the prompt rather than setting `$env:OMNISMITH_ACCESS_TOKEN`: PowerShell
saves typed commands to its history file. For a scripted install the variable works too.

| Task | How |
|------|-----|
| Configure | `C:\ProgramData\omnistat\omnistat.yaml` (optional; only administrators can edit it), then restart the service |
| Upgrade | run `service install` from the new version: the binary is replaced, settings are kept |
| Rotate the token | `service install --replace-token` |
| Change the proxy | set `$env:HTTPS_PROXY`, then `service install` |
| Start / stop | Services, `Start-Service omnistat`, `Stop-Service omnistat` (a stop publishes what is buffered first) |
| Logs | Event Viewer → Windows Logs → Application, source `omnistat` |
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

An attribute may declare the platforms it can be collected on. **Load averages are not
collected on Windows**, which maintains no load average — omnistat says so once at
startup and publishes nothing for them rather than substituting the nearest available
number. The attributes are still declared in the project schema everywhere, so a mixed
fleet converges on one schema. Likewise **macOS publishes only `mem_total_mib`**: it
maintains no estimate of memory available without swapping, and a figure computed from its
page counts would overstate the headroom.

The host identity is derived from the OS machine id (`/etc/machine-id` on Linux,
the platform UUID on macOS) as a keyed hash — the raw id is never published. Pin it
for clones or containers with `OMNISTAT_IDENTITY=…` or `identity.static` in the config.

Optional `omnistat.yaml` (picked up from the working directory, or `--config`):

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
identity:
  static: rack7-node3    # optional: pin the identity instead of autodiscovering it
http: { timeout: 15s, retries: 3 }
log:  { level: info, format: text }
```

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
