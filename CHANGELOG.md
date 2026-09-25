# Changelog

All notable changes to this project are documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning: [SemVer](https://semver.org/).

## [Unreleased]

### Added
- omnistat runs on Windows (feature 006, ADR-0010). The host identity comes from Windows' machine GUID, derived like the other sources (`windows-machine-guid`).
- `omnistat service install` (Windows) installs omnistat as a service:
  - copies the binary to `Program Files`;
  - runs it as `NT SERVICE\omnistat` with a delayed automatic start and a restart one minute after any failure;
  - stores the token and settings as the service's environment, readable only by Administrators and SYSTEM, with the token asked for without echo;
  - checks the token, project and identity before changing anything.

  Running it again upgrades the service; `--replace-token` rotates the token, and `--dry-run` shows every change. `omnistat service uninstall` removes it all except the config directory (ADR-0011).
- As a service, omnistat logs to the Windows Application log (source `omnistat`) and reads `%ProgramData%\omnistat\omnistat.yaml`. A stop, shutdown or preshutdown request publishes what is buffered, as SIGTERM does.
- CI runs the unit tests on Windows, and `make lint`/`make crosscheck` also check the Windows build.
- Spec 006 (omnistat on Windows); ADR-0010 (Windows is a supported platform; constitution 1.1.0); ADR-0011 (the Windows service's security model).
- Release pipeline: pushing a `vX.Y.Z` tag publishes a GitHub Release with static binaries for linux, darwin and windows on amd64 and arm64 (`tar.gz`, `zip` on Windows), plus `checksums.txt`. The tag's CHANGELOG section becomes the release notes. GoReleaser (`.goreleaser.yaml`) and `.github/workflows/release.yml`; `make release-snapshot` and `make release-check` run it locally.
- `memory` module: `mem_used_pct` and `mem_available_mib` as metrics and `mem_total_mib` as a dimension, collected every 30s by default (feature 005). "Available" is the OS's own estimate of memory usable without swapping; amounts are whole MiB. macOS publishes only the total, because it maintains no available-memory estimate.
- Spec 005 (`memory` module); ADR-0009 (host readings live in one core package).
- `cpu` module — omnistat's first metric provider: `cpu_usage_pct` and the `load_avg_1/5/15` averages as metrics, `cpu_model`, `cpu_cores` and `cpu_arch` as dimensions, collected every 10s by default (feature 004).
- Per-attribute platform support in manifests: an attribute declares where it can be collected, is skipped elsewhere with one startup message, and is declared in the schema everywhere so a mixed fleet converges on one schema (ADR-0007). Load averages are not collected on Windows, which maintains none.
- Rate providers may keep the previous counter reading and prime themselves on the first call, so a one-shot `omnistat run` publishes a real CPU measurement rather than nothing (ADR-0006).
- `make crosscheck`: the binary must build `CGO_ENABLED=0` for linux, darwin and windows on amd64 and arm64.
- Spec 004 (`cpu` module); ADR-0006, ADR-0007, ADR-0008.

### Fixed
- `omnistat run --dry-run` on a project whose schema is not applied yet no longer reports `observation dropped` for a list value such as `cpu_arch`. A value whose option the shown plan would create is printed with `(option created by schema apply)`, or `"pending_option": true` in JSON. An option neither present nor planned is still an error (spec 003 FR-021, amended).
- SDK bumped to `github.com/omnismith-sdk/go v1.0.15`. `GetEntityChart`'s `start`/`end` are now `int64`, so `EntityChart` no longer rejects times past January 2038.
- Host-entity resolution no longer creates a duplicate when resolving right after a create. The platform processes writes asynchronously, so a new entity is briefly unsearchable. The re-search after a create now waits (bounded, about 3s) until it can see the entity it created, and then runs its concurrent-creation check (spec 002 FR-012, amended).
- Schema reconciliation tolerates discovery lagging behind its own writes. List item ids are taken from the create response. Ids learned from writes are never dropped by a stale read. Objects this run created are not created again. A fleet-race re-read waits for the object before declaring it absent (spec 001 FR-024/FR-025, amended).
- `cpu_usage_pct` and `mem_used_pct` are published rounded to two decimal places instead of full float precision (spec 004 FR-005, spec 005 FR-007, amended).
- `modules.<name>.enabled: false` failed with `config: override for unknown module "<name>"` for every module. A config block carrying only `enabled` or `interval` was recorded as a schema override and then resolved against the manifests the switch had just removed; switches are no longer treated as overrides (spec 001 FR-006, US-4/1).

### Changed
- Host readings for every value module now come from one core package, `internal/hostread`, the only importer of gopsutil; `cpu` moved onto it with no change in behaviour (ADR-0009). The one-record-per-collection omission report is shared as `module.Omissions`.
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
