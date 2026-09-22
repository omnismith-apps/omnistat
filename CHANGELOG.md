# Changelog

All notable changes to this project are documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning: [SemVer](https://semver.org/).

## [Unreleased]

### Added
- `cpu` module — omnistat's first metric provider: `cpu_usage_pct` and the `load_avg_1/5/15` averages as metrics, `cpu_model`, `cpu_cores` and `cpu_arch` as dimensions, collected every 10s by default (feature 004).
- Per-attribute platform support in manifests: an attribute declares where it can be collected, is skipped elsewhere with one startup message, and is declared in the schema everywhere so a mixed fleet converges on one schema (ADR-0007). Load averages are not collected on Windows, which maintains none.
- Rate providers may keep the previous counter reading and prime themselves on the first call, so a one-shot `omnistat run` publishes a real CPU measurement rather than nothing (ADR-0006).
- `make crosscheck`: the binary must build `CGO_ENABLED=0` for linux, darwin and windows on amd64 and arm64.
- Spec 004 (`cpu` module); ADR-0006, ADR-0007, ADR-0008.

### Fixed
- `modules.<name>.enabled: false` failed with `config: override for unknown module "<name>"` for every module. A config block carrying only `enabled` or `interval` was recorded as a schema override and then resolved against the manifests the switch had just removed; switches are no longer treated as overrides (spec 001 FR-006, US-4/1).

### Changed
- New dependency: `github.com/shirou/gopsutil/v4` supplies host readings for value modules, behind a narrow per-module interface (ADR-0008).

- `omnistat run [--daemon] [--dry-run [--json]]`: reconcile → resolve the host entity → collect every module → publish; one-shot by default (exit 2 when a module failed), daemon mode with per-module collection intervals (`modules.<name>.interval`) and a publish interval (`publish.interval`), collection-time stamps, bounded in-memory buffering through outages, final flush on SIGTERM/SIGINT (feature 003).
- `hostname` module: the host entity's human-readable label (feature 003).
- Provider contract for value modules (`module.Provider`); ADR-0005 (the core owns the clock).
- Spec 003 (run loop, publisher & `hostname` module).
- `machine-id` module and `omnistat identity [--json]`: stable derived host identity (HMAC-SHA256 of the OS machine id, ADR-0004), static override via `identity.static` / `OMNISTAT_IDENTITY`, idempotent host-entity resolution (feature 002).
- `make sandbox`: build-tagged tests against a real project.
- `omnistat schema plan|apply|verify`: module manifests, YAML config with slug/template overrides, additive schema reconciliation with dry-run, conflict detection, fleet-race handling and JSON plan output (feature 001).
- ADR-0002 (attribute-side binding), ADR-0003 (additive-only API interface).
- Clear guidance when the token has no access to the configured project (`project_access_denied`).
- Specs 001 (module manifests & schema reconciliation) and 002 (`machine-id` & host identity) — approved.
- ADR-0001: modular, schema-owning exporter (not blueprint-bound); constitution v1.0.0 ratified.
- Project scaffold: spec-driven layout (`specs/`), constitution skeleton (modular,
  schema-agnostic exporter), templates, agent manual (`AGENTS.md`), CI gate, `omnistat version` stub.
