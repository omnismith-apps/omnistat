# omnistat

A modular [Omnismith](https://omnismith.io) exporter for hosts. Each module
(`machine-id`, `hostname`, `ip-address`, `cpu`, …) declares the templates and
attributes it needs, omnistat reconciles that schema in the target project, then
publishes dimensions and ingests metrics. Slugs have stable defaults and can be
remapped in config to fit an existing schema or marketplace blueprint.

> Status: **scaffolded, no behaviour yet.** This project is developed spec-first —
> see [`specs/README.md`](specs/README.md). The first feature spec is still to be written.

## Why

Omnismith is a dynamic data platform (templates → attributes → entities, with native
metric time series, dashboards and automations). `omnistat` is the first app in the
`omnismith-apps` organisation and doubles as a dogfooding exercise for the official
Go SDK, [`github.com/omnismith-sdk/go`](https://github.com/omnismith-sdk/go).

## Quick start (for now)

```bash
make build && ./bin/omnistat version
```

Configuration is read from the environment — copy `.env.example` to `.env`
(git-ignored) and fill in an `omni_…` access token and a project id.

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
