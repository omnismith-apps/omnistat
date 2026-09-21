---
feature: 002-host-identity
status: done              # draft | approved | done
approved: 2026-09-22
spec: ./spec.md
created: 2026-09-22
depends_on: [001-module-schema-reconciliation]
---

# Plan: `machine-id` module & host identity

## Constitution check
- [x] Uses the SDK for all API access (II) — two new `omni.API` methods, SDK-backed.
- [x] Every template/attribute lives in exactly one module manifest with default slugs (III) — `machine_id` lives in `internal/module/machineid`.
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV) — new API methods: search, create entity. No delete.
- [x] No secret can reach a commit, a flag, or a log line (IV) — raw OS id is never logged (FR-018); static identity via env/YAML only.
- [x] Every mutation is covered by dry-run (IV) — `omnistat identity` shows the resolution without writing; `Resolve` has a `dryRun` flag used by it.
- [x] Every FR/NFR has a test strategy using a fake API (V) — `omnitest` gains entity search/create.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module imports (VI) — no new dependencies; `machineid` imports only `manifest`.

## Technical context
- Builds on 001: `schema.Resolved` supplies the host template id and the `machine_id`
  attribute id/slug (FR-010 needs both).
- API operations:
  | Operation | SDK call | Used for |
  |-----------|----------|----------|
  | `searchEntities` | `EntityAPI.SearchEntities(ctx, templateID).SearchEntitiesRequest({filter_groups: [[{field: <identity slug>, operator: "eq", value: <identity>}]]}).SortField("created_at").SortDirection("asc").Limit(10)` | FR-010, FR-013 (oldest first) |
  | `createEntity` | `EntityAPI.CreateEntity(ctx, <host template slug>).CreateEntityRequest({attributes: {<identity slug>: <identity>}})` | FR-012 |
- OS sources: Linux `/etc/machine-id` then `/var/lib/dbus/machine-id`; macOS
  `ioreg -rd1 -c IOPlatformExpertDevice` parsed for `IOPlatformUUID` (subprocess, since
  `CGO_ENABLED=0` rules out IOKit). Other GOOS → "unsupported" → FR-006 error.
- Derivation (FR-007): `HMAC-SHA256(key = "omnistat/host-identity/v1", msg = trimmed raw id)`,
  lowercase hex (64 chars). Mirrors systemd's `sd_id128_get_machine_app_specific` intent.

## Approach
1. **Module** `internal/module/machineid`: `Manifest()` declares `machine_id` (`text`, on
   host template). The provider exposes `Discover(fs, runner) (Identity, error)` where
   `Identity{Value, Source}`; file system and command runner are injected for tests.
   Static override short-circuits discovery (FR-005). Empty/whitespace/all-zero → absent.
2. **Core resolution** `internal/identity`: `Resolve(ctx, api, resolved, id, dryRun) (Host, error)`
   implementing FR-010…013: search → 0: create, re-search; 1: use; >1: oldest + warn.
   `Host{EntityID, Identity, Created bool, Duplicates []string}` is held in memory only (FR-015).
3. **API surface**: add `FindEntities(ctx, templateID, attrSlug, value) ([]EntitySummary, error)`
   and `CreateEntity(ctx, templateSlug, attrs map[string]any) (id string, error)` to
   `omni.API`; `EntitySummary{ID, CreatedAt}`. Fake server gets an entity store with
   filter `eq` on one field and `created_at asc` ordering, plus `RaceOnCreateEntity`.
4. **Always-on**: `module.Registry` marks `machine-id` as `required`; config `enabled: false`
   on it is a validation error (FR-002).
5. **CLI**: `omnistat identity [--json]` prints value, source, and — if the project is
   reachable and the schema resolved — entity id / "would create" / duplicates (FR-017).
   In the long-running command (feature 003) resolution runs after reconciliation and before
   publishing (FR-016).
6. **Config**: `identity.static` in YAML, `OMNISTAT_IDENTITY` env wins (FR-009); max 128 chars.

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/module/machineid` | Manifest + provider (discovery, derivation) | FR-001, FR-004…008 |
| `internal/identity` | Entity resolution (search/create/select oldest) | FR-010…016 |
| `internal/omni` (extend) | `FindEntities`, `CreateEntity`, entity error mapping | FR-010, FR-012 |
| `internal/omni/omnitest` (extend) | Entity store, filter, race injection | tests |
| `internal/cli` (extend) | `identity` subcommand | FR-017 |
| `internal/config` (extend) | `identity.static`, env | FR-008, FR-009 |

## Data flow
`identity` / long-running start: config → identity (static or discover→derive) →
(after 001 reconcile) `Resolve` → search by `machine_id == value` on host template →
create if none → re-search → pick oldest → hold entity id for the run.

## Configuration
| Setting | YAML | Env | Default | Requirement |
|---------|------|-----|---------|-------------|
| Static identity | `identity.static` | `OMNISTAT_IDENTITY` | none (autodiscover) | FR-008, FR-009 |
| Identity attribute slug/template | `modules.machine-id.attributes.machine_id.{slug,template}` | — | `machine_id` / host | FR-003 |

## Testing strategy
| Requirement | Test type | Where |
|-------------|-----------|-------|
| FR-004 file precedence, trimming, all-zero, missing | unit with `fstest.MapFS` | `internal/module/machineid/discover_test.go` |
| FR-004 macOS `ioreg` parsing | unit with fake runner + fixture output | same |
| FR-005/006/008/009 static override, empty, >128, env wins | unit | `internal/config`, `machineid` |
| FR-007 derivation vector (known raw → known hex), stability | unit, golden | `machineid/derive_test.go` |
| FR-010…013 resolution: 0/1/>1 matches, oldest selection, warn | integration via `omnitest` | `internal/identity/resolve_test.go` |
| US-1/3 concurrent first run converges to one entity | `-race` test, two goroutines + `RaceOnCreateEntity` | `internal/identity/resolve_race_test.go` |
| FR-014 create payload contains only identity | fake server request capture | same |
| FR-017 `identity` command output/exit codes, no writes | CLI test | `internal/cli/identity_test.go` |
| FR-018 raw id never logged | slog capture | `internal/cli/logging_test.go` |

## Risks & unknowns
- `searchEntities` on a `text` attribute with `eq`: assumed exact, case-sensitive match;
  verified against the fake only until the first sandbox run — add a manual check to tasks.
- macOS discovery depends on `ioreg` output format; guarded by a fixture and a clear
  FR-006 error if parsing fails.
- Containers on Linux may expose the *host's* `/etc/machine-id` (bind mount) — same entity
  as the host; documented, static override is the remedy.

## Decisions taken here that deserve an ADR
- ADR-0004: identity derivation scheme (HMAC-SHA256, fixed key, hex) is a stable public
  contract; changing it requires a new `Source` value and a migration note.
