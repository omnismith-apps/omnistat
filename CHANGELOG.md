# Changelog

All notable changes to this project are documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning: [SemVer](https://semver.org/).

## [Unreleased]

### Added
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
