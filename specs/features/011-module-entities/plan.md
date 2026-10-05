---
feature: 011-module-entities
status: done             # draft | approved | done
approved: 2026-10-05
spec: ./spec.md
created: 2026-10-05
depends_on: [001-module-schema-reconciliation, 002-host-identity, 003-run-loop-publisher]
---

# Plan: keyed entities — module-owned records, and the host found by its platform key

## Constitution check
- [x] Uses the SDK for all API access (II). New operations: `getEntityByKey`,
  `upsertEntityByKey`, `updateEntity` with `external_key`, and `createAttribute` with
  `reference_config`. All are in SDK v1.0.18, now pinned.
- [x] Every template/attribute lives in exactly one module manifest with default slugs;
  no hard-coded schema outside manifests (III). The host link is declared by the module
  that owns the entity template. Its display attribute is picked through a **label rank**
  that manifests declare (`hostname` 2, `machine_id` 1), not by naming another module's
  slug in the core.
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV). The
  only new write to an existing record is setting a key on a host entity that holds
  none (FR-012). The identity API seam has no method that clears or changes a set key.
- [x] No secret can reach a commit, a flag, or a log line (IV). Keys are the derived
  host identity and device serials; neither is a secret (002 FR-007, 012 FR-008).
- [x] Every mutation is covered by dry-run (IV). Resolution reports found, would adopt
  and would create without writing. The dry-run printer shows each module entity
  (FR-020).
- [x] Every FR/NFR has a test strategy using a fake API (V). `omnitest` gains by-key
  lookup, upsert, `external_key` on PATCH, the partial unique index and the upsert race.
  See the table below.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI). No new package. `identity` grows from host resolution to keyed resolution
  (FR-011, FR-012), and `collect` gains a per-entity buffer set (FR-016). The fixture
  module that has an entity template lives in `moduletest`.

## Technical context
- Go 1.26; SDK `github.com/omnismith-sdk/go v1.0.18` (bumped from v1.0.15 on
  2026-10-05; the two breaking changes are recorded in the API notes).
- API facts (server source `api-ng`, 2026-10-05):
  - `external_key` lives on the entity row, under a partial unique index on
    `(template_id, external_key)` for live records. Upsert and lookup both read that
    row, so a lookup sees an upsert's result at once. The asynchronous lag only affects
    search and the read models.
  - `PUT /entities/template/{t}/by-key` answers 201 (created) or 200 (updated) with
    `{id, created}`. `attributes` may be `{}`. A racing create is retried server-side as
    an update, and a 409 means "send it again".
  - `GET /entities/template/{t}/by-key?key=` answers the record, or 404.
  - `PATCH /entities/{id}` with only `external_key` is valid (`attributes` became
    optional). A 409 means another live record holds the key.
  - A reference attribute is created with `attribute_type: 3` and
    `reference_config {target_template_id, target_attribute_id}`, both required.
    Discovery reports `reference {target_template_id, target_template_slug,
    target_attribute_id}` and `type: "reference"`.
  - A reference value is written as the referenced entity's UUID.
- The local API was down while planning, so the spec's two platform checks (an upsert
  is visible to a lookup at once; which automation events an upsert fires) rest on the
  server source. They are rechecked by the sandbox tests when the API is up (T011).

## Approach

### 1. Manifest (FR-001…FR-005)
- New kind `reference`. `Attribute.Target` names the template a reference points to.
  Only `manifest.HostTemplate` is accepted (FR-002). Every other kind must leave it empty.
- `Template.Entity bool` marks an entity template. Validation (001 FR-004 extended):
  an entity template has exactly one reference attribute on it (its host link); a
  reference attribute must sit on an entity template; the host template cannot be one.
- `Attribute.Label int`: a label rank for the template the attribute attaches to.
  `hostname` declares 2 and `machine_id` declares 1. The display attribute of a host link
  is the highest-ranked label bound to the resolved host template among the enabled
  modules (FR-003).
- `Resolve` gains the override rules of FR-005:
  - a module-level template override on a module whose attributes all sit on its one
    entity template **renames** that template. It is declared under the new slug, with
    the manifest's name and description;
  - on any other module with an entity template it is an error;
  - a per-attribute template override on an entity-template attribute must name the
    entity template's resolved slug;
  - an entity template may not resolve to the host slug, and two modules' entity
    templates may not resolve to the same slug.
- `DesiredTemplate` gains `Entity`, `Module` and `Key` (the manifest's default slug, the
  provider's stable name for it). `DesiredAttribute` gains `EntityTemplate` (that key,
  for attributes on an entity template), `Target` and `Display` (resolved slugs for a
  reference). `Desired.HostLink(templateSlug)` returns the host link attribute.

### 2. Schema (FR-002…FR-004)
- `CurrentAttribute.RefTemplateID` comes from discovery's `reference`.
- `Diff`: a missing reference becomes a *create attribute* action carrying `Target`
  and `Display`. References are ordered **after** the other attribute creations, so their
  display attribute exists first (the order stays deterministic, 001 FR-019). An existing
  attribute matches when its type is `reference` and it targets the resolved host
  template's id. Otherwise it is a conflict (`expected "reference → host"`). The display
  attribute is never compared.
- `Apply` passes the target template id and display attribute id from `Resolved`. The
  plan text shows `+ attribute ups_host (reference → host) → ups`, and JSON adds
  `target` and `display`. Version 1 is kept, since fields are only added.

### 3. Omni client and fake API
- `ReadSchema` reads `reference.target_template_id`. `CreateAttribute` maps `reference`
  to `(3, 0)` with `reference_config`.
- New methods: `EntityByKey(template, key) (id, found, err)`; `UpsertByKey(template,
  key, attrs) (id, created, err)`; `SetEntityKey(id, key) error`. `FindEntities` also
  returns each match's `external_key`. A 409 on upsert or set-key maps to
  `identity.ErrKeyTaken`.
- `omnitest`: entities carry `ExternalKey`, with a uniqueness check among live records
  of a template; `GET`/`PUT /entities/template/{t}/by-key`; PATCH accepts
  `external_key` (409 when held) and `attributes` becomes optional. `UpsertRace` makes
  the next upsert answer 409 once (FR-011's retry). Search results carry
  `external_key`. Reference attributes are stored with their target, and discovery
  reports it.

### 4. Identity (FR-007, FR-008, FR-010…FR-015, FR-022)
- `identity.API` becomes `FindEntities`, `EntityByKey`, `UpsertByKey` and
  `SetEntityKey`. `CreateEntity` leaves the seam: resolution never creates without a key
  any more.
- `ResolveHost(ctx, api, target, key, dryRun, log)`: lookup → adoption search (oldest;
  duplicates listed; empty key → set it, with dry-run reporting *would adopt*; a different
  key → use and warn; set-key conflict → lookup) → upsert `{identity attribute}`. The
  outcome is one of `found`, `adopted`, `adopted-other-key`, `created`, `would-adopt` or
  `would-create`. No settle wait remains: neither the lookup nor the upsert lags (see
  Technical context).
- `Keyed`: the module-entity resolver used by the publisher. It caches template+key → id
  for the process (FR-013), resolves by lookup then upsert `{host link: host id}`, and
  reports `would-create` in dry-run. `Forget` drops one id (FR-015). Upsert conflicts
  are retried three times, then resolved by lookup (FR-011).
- `ValidKey` implements FR-008: trimmed, 1–128 characters, no control characters.

### 5. Collect (FR-006…FR-009, FR-016)
- `module.Observation` gains `Entity module.Entity{Template, Key}`. The zero value is the
  host.
- `collect.Source.Attrs` keeps reference attributes out (the provider never emits them,
  and they do not make a module collectable). An observation for a reference key, or one
  whose target template does not match the attribute's `EntityTemplate`, is dropped with
  an error log (FR-006). Invalid keys are counted and logged once per collection per
  module (FR-008).
- `Sample.Target collect.Target{Module, Template (resolved slug), Key}`.
- `collect.Buffers` implements a `Sink`: the host's `Buffer` plus one `Buffer` per
  target, created on first use, capped at 256 targets per module (FR-009, one warning
  per module per process). `Targets()` lists module targets in a stable order, and
  `Drops()` aggregates. `Scheduler` and `Once` take a `Sink`, so the existing
  single-buffer tests keep passing a `*Buffer`.

### 6. Publish (FR-015…FR-021, FR-023)
- `publish.Entities` coordinates one publish over the host buffer and every target
  buffer:
  - the host is published by the existing `Publisher`, and its `ErrEntityGone` still
    ends the run;
  - for each target with something buffered, it resolves the id through a `Resolver`
    interface (implemented by `identity.Keyed`) and publishes with a `Publisher` for that
    entity. The host link (`Backfill{host id}`) is added to every non-empty dimension
    update (FR-018);
  - `ErrEntityGone` on a target → `Forget`, warn, keep the buffer (FR-015). Any other
    failure is logged per entity and the rest continue (FR-014, FR-017).
  - `Result` gains `Entities` and `EntitiesFailed`.
- Dry-run: the printer prints one block per entity, with template, key and
  `exists`/`would-create`, plus the host link. JSON documents gain `template`, `key` and
  `would_create`; there is one document per entity per publish, and the version stays 1.

### 7. CLI (FR-010, FR-020…FR-023, NFR-004)
- `run`: host resolution through `identity.ResolveHost`; a `Buffers` sink; publishing
  through `publish.Entities`. The one-shot exit status is partial when a module entity
  failed (FR-021). The daemon summary adds entity counts (FR-023).
- `identity` (inspection): reports `found`, `would adopt <id>`, `would create`, or
  `adopted entity holds another key`. JSON adds `outcome` and keeps `would_create` and
  `duplicates`.
- The service install pre-check uses the same read-only path (unchanged behaviour).

## Package layout (delta)
| Package | Change | Justified by |
|---------|--------|--------------|
| `manifest` | reference kind, entity templates, label rank, FR-005 override rules | FR-001…FR-005 |
| `schema` | reference matching, creation order, rendering | FR-002…FR-004 |
| `omni`, `omni/omnitest` | keyed entity operations; reference attributes; fake by-key API | FR-011, FR-012, NFR-005 |
| `identity` | `ResolveHost` (adoption), `Keyed` resolver, `ValidKey` | FR-007…FR-015 |
| `module` | `Observation.Entity` | FR-006 |
| `collect` | `Target`, `Buffers`, `Sink`; target validation | FR-006…FR-009, FR-016 |
| `publish` | `Entities` coordinator; per-entity printing | FR-015…FR-021, FR-023 |
| `module/moduletest` | fixture module `gadget` with entity template `gadget` | NFR-005 |
| `cli` | wiring, inspection, exit status, summary | FR-010, FR-020…FR-023 |

## Data flow
1. Startup: config → manifests → `Resolve` (FR-005 checks) → schema read → plan/apply
   (references last) → `ResolveHost`: lookup by key → [adopt by `machine_id`] → upsert.
2. Collection: the provider returns observations, some with `Entity{Template, Key}`.
   `collect` validates the key and template, then routes each sample to the host buffer
   or its target buffer.
3. Publish: host → `Publisher`. For each target: `Keyed.Resolve` (cached; lookup →
   upsert `{host link}`) → `Publisher` with the host link added to the dimensions →
   ack per target buffer.

Idempotency: the platform's unique (template, key) index. Retries: the transport's
policy for 5xx/429, plus three upsert retries on 409.

## Configuration
None new. Entity templates and host links are remapped with the existing
`modules.<name>.template` and `modules.<name>.attributes.<key>.{slug,template}`.

## Testing strategy
| Requirement | Test type | Where |
|-------------|-----------|-------|
| FR-001, FR-002 | unit (validation table) | `manifest/validate_test.go` |
| FR-003, FR-005 | unit (resolve table: label rank, renames, errors) | `manifest/resolve_test.go` |
| FR-003, FR-004 | unit (diff: create, match, conflict, order) + apply against fake | `schema/diff_test.go`, `schema/apply_test.go` |
| FR-006…FR-009 | unit (routing, drops, cap) | `collect/scheduler_test.go`, `collect/buffers_test.go` |
| FR-011, FR-012 | fake API: found, adopt, adopt-other-key, duplicates, set-key race, fresh create, upsert 409 | `identity/resolve_test.go` |
| FR-013…FR-015 | fake API: cache, failure isolation, 404 → recreate | `identity/keyed_test.go`, `publish/entities_test.go` |
| FR-016…FR-019 | fake API: per-entity buffers, host link, 422 per entity | `publish/entities_test.go` |
| FR-020 | dry-run text and JSON, inspection | `cli/run_test.go`, `cli/identity_test.go` |
| FR-021, FR-023 | one-shot exit status, daemon summary | `cli/run_test.go`, `cli/run_daemon_test.go` |
| NFR-001 | request counting on the fake | `identity/resolve_test.go`, `cli/run_test.go` |
| NFR-003 | seam has no clear/change-key method; adoption never overwrites | `identity/resolve_test.go` |
| NFR-004 | existing CLI suites unchanged; `machine_id` still set on create | `cli/*_test.go` |
| NFR-006 | sandbox: adoption of a legacy host, fresh keyed host, UPS entity (012) | `cli/sandbox_test.go`, `cli/sandbox_ups_test.go` |

## Risks & unknowns
- **Automations on upsert.** An upsert-create may fire `on_entity_created` and
  `on_attribute_changed` for the attributes it sets, as a create does. Lookup-first
  keeps steady-state starts write-free (spec decision). To be confirmed in the sandbox.
- **Reference creation needs both ids.** If the label attribute is renamed or missing
  in a project, creation fails with a 422. The display attribute comes from `Resolved`
  after the non-reference attributes are created, so a fresh project is covered.
- **Downgrade.** Earlier versions ignore keys; they find the same entity by
  `machine_id` (spec edge case). Nothing to do.

## Decisions taken here that deserve an ADR
- **ADR-0014** — entities are identified by the platform's external key (host and
  module-owned), resolved by lookup then upsert, with adoption of legacy hosts and never
  overwriting a key; module-owned entity templates link to the host by a reference whose
  display attribute is chosen by label rank.
