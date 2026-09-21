# Changelog

All notable changes to this project are documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning: [SemVer](https://semver.org/).

## [Unreleased]

### Added
- `omnistat schema plan|apply|verify`: module manifests, YAML config with slug/template overrides, additive schema reconciliation with dry-run, conflict detection, fleet-race handling and JSON plan output (feature 001).
- ADR-0002 (attribute-side binding), ADR-0003 (additive-only API interface).
- Clear guidance when the token has no access to the configured project (`project_access_denied`).
- Specs 001 (module manifests & schema reconciliation) and 002 (`machine-id` & host identity) — approved.
- ADR-0001: modular, schema-owning exporter (not blueprint-bound); constitution v1.0.0 ratified.
- Project scaffold: spec-driven layout (`specs/`), constitution skeleton (modular,
  schema-agnostic exporter), templates, agent manual (`AGENTS.md`), CI gate, `omnistat version` stub.
