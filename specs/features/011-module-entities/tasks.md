---
feature: 011-module-entities
status: done             # draft | in-progress | done
plan: ./plan.md
---

# Tasks: keyed entities — module-owned records, and the host found by its platform key

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

Every task ends green on `make all` unless it says otherwise.

## Phase 0 — De-risking

- [x] **T001** *(spike, read-only)* Read `api-ng`'s upsert-by-key, lookup-by-key and
  reference-attribute code; record the facts in the plan and the API notes. Running the
  same checks against the local API is folded into T011 (the API was down).
  *Verify:* plan "Technical context".
- [x] **T002** Bump the SDK to v1.0.18, fix the two breaking changes, and record them in
  the API notes. *Verify:* `make all`, `make crosscheck`.

## Phase 1 — Schema

- [x] **T003** *(tests first)* `manifest`: the `reference` kind with `Target`;
  `Template.Entity`; `Attribute.Label`; validation of entity templates and host links;
  `Resolve`'s FR-005 rules (rename by module override, per-attribute pinning, host
  collision, two modules on one slug), the display attribute by label rank, and
  `EntityTemplate`/`Target`/`Display` on desired attributes. Label ranks on `hostname`
  (2) and `machine_id` (1). *FR-001…FR-005.* *Verify:* `manifest` table tests.
- [x] **T004** *(tests first)* `schema` + `omni` + `omnitest`: `RefTemplateID` from
  discovery; reference creation with `reference_config`; references ordered after the
  other attributes; matching and the "reference → host" conflict; plan text and JSON.
  *FR-002…FR-004.* *Verify:* `schema` diff/apply tests against the fake; `omni`
  client test for the request body.

## Phase 2 — Keyed resolution

- [x] **T005** *(tests first)* `omnitest`: external keys with a live-record uniqueness
  check, `GET`/`PUT /entities/template/{t}/by-key`, `external_key` on PATCH (409 when
  held, `attributes` optional), `external_key` in search results, `UpsertRace`.
  `omni`: `EntityByKey`, `UpsertByKey`, `SetEntityKey`, `FindEntities` with keys; a 409
  maps to `identity.ErrKeyTaken`. *FR-011, FR-012, NFR-005.* *Verify:* `omni` client
  tests.
- [x] **T006** *(tests first)* `identity`: `ResolveHost` (found, adopt, adopt with
  duplicates, adopted entity holding another key, set-key conflict, fresh create, upsert
  409 retry, dry-run would-adopt and would-create, request counts) and `Keyed` (cache,
  lookup then upsert with the host link, `Forget`, dry-run), `ValidKey`. Remove the old
  search/settle resolution and its lag/race tests, which no longer apply. *FR-007,
  FR-008, FR-010…FR-015, NFR-001, NFR-003.* *Verify:* `identity` tests.

## Phase 3 — Collect and publish

- [x] **T007** *(tests first)* `module.Observation.Entity`; `collect.Target`, `Sample.Target`,
  `Buffers` (per-target buffers, 256-key cap with one warning per module, drops) and the
  `Sink` interface; `Sources` leaves references out; `collectOne` routes and validates
  targets (FR-006 template match, FR-007 reference dropped, FR-008 one log per
  collection). *FR-006…FR-009, FR-016.* *Verify:* `collect` tests.
- [x] **T008** *(tests first)* `publish.Entities`: host first, then targets in order;
  the host link on every dimension update; 404 → `Forget` and keep; failure isolation;
  422 per entity; `Result` entity counts; per-entity dry-run printing (text and JSON).
  *FR-014…FR-020, FR-023.* *Verify:* `publish` tests against `omnitest`.

## Phase 4 — CLI

- [x] **T009** `cli`: `run` uses `ResolveHost`, `Buffers` and `Entities`; one-shot
  partial exit on entity failure; daemon summary counts; `identity` inspection outcomes
  (text and JSON). `moduletest.Gadget` fixture (entity template `gadget`). Update the
  existing CLI tests to the keyed host. *FR-010, FR-020…FR-023, NFR-004.* *Verify:*
  `cli` tests, including an end-to-end one-shot and daemon run of `Gadget` against the
  fake.
- [x] **T010** ADR-0014; `AGENTS.md` §5 and the API notes (keys, references);
  `internal/README.md`; `CHANGELOG.md`. *Verify:* `make specs-check`.

## Phase 5 — Acceptance and sync

- [x] **T011** Sandbox (local API): a legacy host entity (no key, `machine_id` set) is
  adopted, not duplicated; a fresh project gets a keyed host; an upsert is visible to a
  lookup at once. Run together with 012's sandbox test. *NFR-006.* *Verify:* `make
  sandbox` — **requires the local API**.
- [x] **T012** Sync: spec `status: implemented` with implementation notes, plan `done`,
  ADR-0014 accepted (owner), CHANGELOG. *Verify:* `make specs-check`.
