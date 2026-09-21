# omnistat

A modular [Omnismith](https://omnismith.io) exporter for hosts. Each module
(`machine-id`, `hostname`, `ip-address`, `cpu`, …) declares the templates and
attributes it needs, omnistat reconciles that schema in the target project, then
publishes dimensions and ingests metrics. Slugs have stable defaults and can be
remapped in config to fit an existing schema or marketplace blueprint.

> Status: **early.** Developed spec-first — see [`specs/README.md`](specs/README.md).
> Feature 001 (schema reconciliation) is implemented; feature 002 (host identity) is next.

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
```

Optional `omnistat.yaml` (picked up from the working directory, or `--config`):

```yaml
project_id: 01a0c47a-...
schema:
  mode: apply            # apply | verify | off
  host_template: server  # remap the default `host` template to an existing one
modules:
  cpu:
    enabled: true
    attributes:
      usage: { slug: cpu_usage }   # fit an existing attribute
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
