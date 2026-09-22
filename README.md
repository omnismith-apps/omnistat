# omnistat

A modular [Omnismith](https://omnismith.io) exporter for hosts. Each module
(`machine-id`, `hostname`, `ip-address`, `cpu`, …) declares the templates and
attributes it needs, omnistat reconciles that schema in the target project, then
publishes dimensions and ingests metrics. Slugs have stable defaults and can be
remapped in config to fit an existing schema or marketplace blueprint.

> Status: **early.** Developed spec-first — see [`specs/README.md`](specs/README.md).
> Features 001 (schema reconciliation), 002 (host identity), 003 (run loop, publisher,
> `hostname` module) and 004 (`cpu`, the first metric provider) are implemented; the next
> specs add further value modules (`ip-address`, `memory`, `disk`, …).

## Why

Omnismith is a dynamic data platform (templates → attributes → entities, with native
metric time series, dashboards and automations). `omnistat` is the first app in the
`omnismith-apps` organisation and doubles as a dogfooding exercise for the official
Go SDK, [`github.com/omnismith-sdk/go`](https://github.com/omnismith-sdk/go).

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

### Modules

| Module | Publishes | Default cadence |
|--------|-----------|-----------------|
| `machine-id` | `machine_id` (text) — the host entity's idempotency key; always enabled | once, at startup |
| `hostname` | `hostname` (text) — the entity's human-readable label | 5m |
| `cpu` | `cpu_usage_pct`, `load_avg_1`, `load_avg_5`, `load_avg_15` (metrics); `cpu_model`, `cpu_cores`, `cpu_arch` (dimensions) | 10s |

CPU usage is the non-idle share of the CPU time that elapsed since the previous reading,
aggregated across every logical CPU, so a fully busy 8-core host reports 100, not 800.
The first collection of a process measures over a short 250ms window so that a one-shot
`omnistat run` publishes a real number; every later one spans the whole interval.

An attribute may declare the platforms it can be collected on. **Load averages are not
collected on Windows**, which maintains no load average — omnistat says so once at
startup and publishes nothing for them rather than substituting the nearest available
number. The attributes are still declared in the project schema everywhere, so a mixed
fleet converges on one schema.

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

## License

TBD.
