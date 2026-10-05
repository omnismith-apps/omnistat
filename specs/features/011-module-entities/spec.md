---
feature: 011-module-entities
status: implemented      # draft | review | approved | implemented | superseded
approved: 2026-10-05
implemented: 2026-10-05
created: 2026-10-05
owners: [evgenii]
supersedes: null
adr: [0001, 0003, 0004, 0005, 0014]
depends_on: [001-module-schema-reconciliation, 002-host-identity, 003-run-loop-publisher]
---

# Feature: keyed entities — module-owned records, and the host found by its platform key

## Summary

Every value omnistat publishes today lands on one record: the host entity (002, 003).
That is right for CPU, memory, disk and network totals. It is wrong for a **device the
host manages** that has an identity of its own. A UPS on the host's USB port is the
first case (spec 012). Per-filesystem, per-disk and per-interface records are the next,
deferred by 008 and 010 for exactly this reason: the core cannot identify a second
entity, link it to the host, or publish to it.

Omnismith now gives every entity an optional **external key**: the id another system
uses for it, unique among the live records of its template and enforced by the
platform at write time. Its *upsert by key* creates the record holding a key, or
updates the one that does, in one atomic request. That is exactly the idempotency 002
built by hand: a `machine_id` attribute, a search, a create, a wait for the search
to catch up, and "oldest wins" when two processes raced.

This feature makes the external key omnistat's way of identifying **every** record it
owns:
- A module can declare a template of its own (an *entity template*) with a *host link*
  (a reference to the host). Its provider labels observations with a key. The core
  finds or creates the entity holding that key, links it to the host and publishes its
  dimensions and metrics.
- The **host entity** is found by its key as well, its identity from 002 unchanged.
  Hosts created by earlier versions are **adopted**: found once by `machine_id`, then
  given their key. `machine_id` stays declared and published.

Nothing is ever deleted. Modules that publish only to the host behave exactly as before.

## Users & context

- **Operator.** Wants a device (a UPS now, disks or NICs later) to be a record of its own
  in Omnismith: its own page, history and automations, and a link back to the host
  that reports it. Expects one record per real device and per host across restarts,
  upgrades, reinstalls of omnistat and concurrent starts. After upgrading, expects
  every existing host to keep its record and history.
- **Integrator.** Feeds the same templates from other systems through inbound endpoints
  keyed by external key (e.g. a UPS network card's webhook keyed by serial number).
  Keys are not namespaced, so such a source can deliberately land on the record
  omnistat publishes.
- **Module author.** Wants to say "these values belong to the device with key K" and
  nothing more. Finding, creating, linking, buffering and publishing stay in the core
  (ADR-0005, constitution III).
- **Where it runs.** Wherever omnistat runs. A device's key may be known only after a
  collection (a UPS serial is read from the device), so module entities are resolved
  when first published, not at startup as the host is.
- **Platforms.** Core behaviour, the same on Linux, Windows and macOS. No
  platform-specific code (ADR-0010: nothing to accept per platform beyond the unit tests).

## User stories

### US-1 — A device gets its own record, once (P1)
As an operator, I want each device a module reports to become exactly one entity on the
module's template, so that the device has its own record and history.

**Acceptance scenarios**
1. **Given** a reconciled project with no entity on the module's template, **When** the
   module first reports values for key `K`, **Then** exactly one entity is created on that
   template holding key `K`, with its host link set to the host entity, and the values
   are published to it.
2. **Given** that entity exists, **When** omnistat restarts, is upgraded or is reinstalled,
   **Then** no entity is created and the same entity receives the values.
3. **Given** two omnistat processes reporting key `K` for the first time at the same
   moment, **When** both publish, **Then** exactly one entity holds `K` and both use it.
4. **Given** a module whose key becomes known only after its first successful collection,
   **When** the first collections fail, **Then** no entity is created and nothing is
   published for it until a collection supplies a key.

### US-2 — The device is linked to its host (P1)
As an operator, I want the device's record to reference the host that reports it, so
that I can navigate between them and filter devices by host.

**Acceptance scenarios**
1. **Given** a published device entity, **When** I open it in Omnismith, **Then** its host
   link points to the host entity of the omnistat that published it, shown by the host's
   hostname.
2. **Given** a device moved to another host running omnistat, **When** that host publishes
   it, **Then** the same entity (same key) is reused and its host link now points to the
   new host.

### US-3 — Existing hosts keep their record after the upgrade (P1)
As an operator upgrading from a version that identified hosts by `machine_id` only, I want
each host to keep its entity, so that no history is split and no duplicate appears.

**Acceptance scenarios**
1. **Given** a host entity created by an earlier version (its `machine_id` set, no key),
   **When** the new version starts on that host, **Then** that entity is used, it now
   holds the host's identity as its key, and no entity is created.
2. **Given** two earlier host entities with the same `machine_id` (an old concurrent
   creation, 002 FR-013), **When** the new version starts, **Then** the earliest one is
   adopted and holds the key, the others are reported with their ids and left
   untouched, and every later start finds the adopted one by key.
3. **Given** a fresh project, **When** omnistat starts, **Then** the host entity is
   created holding its key, with `machine_id` set as before.

### US-4 — Nothing is destroyed or rewritten (P1)
As an operator, I want omnistat never to delete records or change keys it did not set,
so that history and other integrations survive.

**Acceptance scenarios**
1. **Given** a device that stops being reported (unplugged, or its source down), **When**
   the daemon keeps running, **Then** its entity stays as it is and receives no values.
2. **Given** a device replaced by one with a different key, **When** the new one is
   reported, **Then** a new entity is created and the old one is left untouched.
3. **Given** a device entity deleted by a user while the daemon runs, **When** the next
   publish for it fails with "not found", **Then** the daemon keeps running, keeps the
   unpublished values, and the next publish creates the entity again with the key.
4. **Given** an earlier host entity whose key was already set to something else (by a
   user or another integration), **When** omnistat adopts it by `machine_id`, **Then**
   it uses that entity, leaves its key unchanged, and warns.

### US-5 — See it before it is written (P2)
As an operator, I want dry-run and the identity inspection to show which entities would
be created, adopted or used, and what would be written to each.

**Acceptance scenarios**
1. **Given** a project without the device's entity, **When** I dry-run, **Then** the output
   names the template, the key and "would be created", plus its host link and its
   values grouped by entity, and nothing is written.
2. **Given** an earlier host entity without a key, **When** I run the identity inspection
   (002 FR-017) or a dry-run, **Then** it reports that the entity would be adopted, with
   its id, and nothing is written.
3. **Given** an empty project, **When** I dry-run `schema plan`, **Then** the plan lists the
   entity template and its host-link reference attribute.

### US-6 — Fit an existing schema (P2)
As an operator with my own schema, I want to remap the entity template and its attributes
like any other, so that devices land in a template I already have.

**Acceptance scenarios**
1. **Given** a module template override to `power_device`, **When** the schema is applied,
   **Then** the template `power_device` is used (created if missing) with the module's
   host link and values, and entities are found by key within it.
2. **Given** an existing attribute with the host link's slug that references a template
   other than the host template, **When** I plan the schema, **Then** it is reported as a
   conflict and apply writes nothing.

## Functional requirements

### Manifest & schema
- **FR-001** A manifest MAY declare one or more **entity templates**. An entity template is
  a template the module declares (001 FR-001) plus exactly one **host link** attribute,
  of kind `reference`, on that template, targeting the host template. Every other
  attribute the module attaches to that template is published to the module's entities
  on it, never to the host entity. The entity's identity is its **external key**
  (FR-007), not an attribute.
- **FR-002** The `reference` kind MUST be accepted in manifests, amending 001 FR-005. In
  this feature a reference attribute MAY target only the host template (after
  overrides). Any other target is rejected by manifest validation (001 FR-004).
- **FR-003** The reconciler MUST create a missing reference attribute with its target
  template set to the resolved host template, and with the host's **label** as its
  display attribute: the `hostname` attribute when that module is enabled, otherwise the
  identity attribute (002 FR-001). This is a *create attribute* action (001 FR-013); no
  new action type is added.
- **FR-004** An existing attribute with the host link's slug MUST match when it is a
  reference to the resolved host template. A different kind, or a reference to another
  template, MUST be a conflict (001 FR-015). A different display attribute is NOT a
  conflict and is never modified, as names and descriptions are not (001 FR-018).
- **FR-005** Overrides (001 FR-007…FR-009) apply to entity templates and their
  attributes with two extra rules, checked at startup before any network call:
  - an entity template MUST NOT resolve to the host template's slug;
  - an attribute of an entity template MUST stay on that template: a per-attribute
    template override naming another template is a fatal configuration error.
  For a module whose attributes all sit on one entity template, a module-level template
  override retargets that template, so its attributes move together. For any other
  module that declares an entity template (several entity templates, or host attributes
  as well), a module-level template override is a configuration error. No module in
  this feature is like that, so its override syntax is left to the first spec that
  needs it.

### Keys
- **FR-006** A provider observation MAY name a **target**: one of its module's entity
  templates (by the key the manifest gives it) and a **key value**. An observation
  without a target goes to the host entity, exactly as in 003. An observation whose
  attribute does not belong to its target's template is dropped with an error log naming
  module, attribute key and template (003 FR-002). The provider never sees entity ids,
  and it MUST NOT emit an observation for the host link: the core sets it, and such an
  observation is dropped with an error log.
- **FR-007** Every entity omnistat owns MUST be identified by its **external key** within
  its template:
  - the host entity's key is the identity of 002 (derived or static, 002 FR-004…FR-009),
    unchanged;
  - a module entity's key is the value its provider supplies (FR-006).
  Keys are used verbatim, with no prefix or namespace. Another system that writes the
  same key to the same template writes to the same record, by design.
- **FR-008** A key MUST be trimmed, non-empty, at most 128 characters and free of control
  characters (within the platform's 255). A module's observations with an invalid key
  are dropped, with one error log per collection naming the module and the reason, not
  the value's full text when longer than 128. An invalid host key is 002 FR-006's fatal
  startup error.
- **FR-009** The core MUST track at most **256** distinct keys per module per process.
  Observations for further new keys are dropped, with a warning logged once per process
  per module. Keys are never forgotten during the process's life.

### Resolution
- **FR-010** The host entity is resolved at startup, after reconciliation (002 FR-016,
  unchanged). A module entity is resolved **lazily**: at the first publish that has
  buffered observations for its key, after the host entity.
- **FR-011** Resolving an entity by key MUST:
  1. look up the live entity of the template holding the key. If one does, it is the
     entity;
  2. otherwise (module entities, and hosts with no earlier entity, FR-012) **upsert by
     key**: one request that creates the entity holding the key, or returns the one that
     took it meanwhile. It is created with only the host link (module entity) or only the
     identity attribute (host entity, 002 FR-014) set. The id comes from the response.
  The platform guarantees at most one live entity per key and template, so no search,
  bounded wait, duplicate detection or "oldest wins" rule is needed for keyed
  resolution. A conflict response on the upsert MUST be retried with the policy of 001
  NFR-003, then resolved by step 1.
- **FR-012** *(Adoption, host only; amends 002 FR-010…FR-013.)* When step 1 of FR-011 finds
  no host entity holding the key, the core MUST first search the host template by the
  identity attribute (`machine_id`), as 002 FR-010 did:
  - one or more matches → take the one created earliest (warning with all matching ids
    when several), and **set its external key** to the identity, then use it;
  - if that entity already holds a **different** key, MUST NOT change it: use the entity
    and warn (US-4/4). The same adoption then repeats at each start;
  - if setting the key conflicts (another entity took the key meanwhile) → resolve by
    FR-011 step 1 and use that entity;
  - no match → FR-011 step 2.
  Module entities have no earlier records and no adoption.
- **FR-013** Resolved entity ids MUST be held for the rest of the process and never
  persisted (002 FR-015). The key is the durable identity.
- **FR-014** A module-entity resolution that fails (API unreachable, timeout, 5xx after
  retries) MUST NOT affect the host entity or other module entities. The entity's
  observations stay buffered, and resolution is retried at the next publish.
- **FR-015** A write to a resolved module entity that fails with "not found" MUST forget
  that entity's id, keep its buffered observations, log a warning naming module,
  template and key, and resolve it again at the next publish (US-4/3). A deleted record
  releases its key, so this creates a new entity. The host entity's loss still ends the
  run (003 FR-015).

### Buffering & publishing
- **FR-016** The buffer of 003 FR-007…FR-009 MUST be kept **per entity**: the bounds apply
  to each (entity, attribute) pair, and an entity's observations are removed only once
  the platform has accepted them for that entity.
- **FR-017** A publish MUST write the host entity first (003 FR-012), then each module
  entity with buffered observations, in a stable order (module, template, key). Each
  entity gets at most one dimension update and its metric ingestions in chunks of at most
  1 000. A failure on one entity MUST NOT prevent the others from being written.
- **FR-018** Every dimension update of a module entity MUST also set its host link to the
  current host entity (US-2/2). It is sent without change detection, as all dimensions
  are (003 FR-016).
- **FR-019** A 422 on a module entity follows 003 FR-015 (drop the named observations,
  never retry the same payload), scoped to that entity.
- **FR-020** Dry-run (003 FR-021) and the identity inspection (002 FR-017) MUST show, per
  entity: template, key, and whether it exists, would be adopted (with its id) or would
  be created. Dry-run also shows each module entity's host link and the values it would
  write, grouped by entity. JSON output MUST carry the target entity of every value.
  Existing JSON consumers keep working: fields are added, not renamed, or the `version`
  field changes.
- **FR-021** The one-shot `run` exit status (003 FR-018) MUST count a module entity that
  could not be resolved or written as a partial run, not a success.

### Observability
- **FR-022** Resolution MUST be logged at info, with template, key, entity id and outcome
  (found, adopted, created, adopted-with-other-key) and, for module entities, the module.
  Keys are logged. The host key is already a derived, non-sensitive value (002 FR-007);
  a module whose key may be sensitive MUST say so in its own spec and hash it.
- **FR-023** The publish log and summary (003 FR-026, FR-026a) MUST count module entities
  written and failed, next to dimensions and observations.

## Non-functional requirements
- **NFR-001** (network) Host resolution at startup: one lookup when the host holds its key;
  one lookup plus one upsert on a fresh project; one lookup, one search and one key update
  for a one-time adoption. A new module key adds one lookup and at most one upsert, once
  per process. Steady-state publishes make at most `1 + E` dimension updates plus
  `ceil(observations ÷ 1 000)` metric requests per entity, for `E` module entities with
  buffered values (amends 003 NFR-003 and 002 NFR-001).
- **NFR-002** (resources) Memory is bounded by FR-009 × the 003 buffer bounds. A daemon that
  cannot reach the API does not grow beyond that.
- **NFR-003** (safety) Only additive operations: create or upsert an entity, partial entity
  update, setting a key on an entity that **has none**, metric ingestion, create
  attribute/template. omnistat never deletes, replaces or retypes, never clears a key,
  and never changes a key that is already set (constitution IV, ADR-0003).
- **NFR-004** (compatibility) A configuration with no entity template produces the same
  schema plan, publish requests and dry-run output (apart from the fields FR-020 adds)
  as before. The only difference is host resolution (FR-011, FR-012). Every host
  entity created by an earlier version is kept and adopted. `machine_id` stays declared,
  published and remappable (002 FR-001…FR-003).
- **NFR-005** (testability) Lookup, upsert, adoption, per-entity buffering and publishing
  are tested against the fake API. That fake MUST enforce one live entity per
  (template, key), answer a racing upsert as the platform does, and keep its search lag
  for the adoption path. Tests cover concurrent first starts, legacy duplicates, a key
  already set, "not found" mid-run and 422 per entity. A fixture module with an entity
  template stands in for real modules.
- **NFR-006** (acceptance) Proven against the sandbox project: a host entity created by
  the previous release is adopted (not duplicated); a fresh project gets a keyed host; and,
  with the first real module (012), the device template and reference attribute are
  created, the entity is created once, linked to the host and read back, and reused
  after a restart.

## Data & integration contract

Read: the project schema (001); the live entity of a template holding a key; for
adoption only, a search of the host template by identity attribute, with creation
timestamps (002 FR-013).

Write (additive only):
- **Schema:** the entity template (create template) and its attributes, including the
  host link as a reference attribute whose target is the host template and whose display
  attribute is the host's label (FR-003).
- **Entities:** upsert by key on the host template with `{identity attribute}`, or on an
  entity template with `{host link}` (FR-011); setting the key on an adopted host entity
  that holds none (FR-012).
- **Dimensions:** partial update of a module entity, including the host link (FR-018).
- **Metrics:** ingestion on the module entity.

Owned attributes: none new. This feature adds capabilities; each module's spec owns its
entity template and host link. `machine_id` remains owned by `machine-id` (002).

## Edge cases & failure modes

- **Two hosts report the same device** (a shared device, two daemons, a misconfigured
  address): one entity; its host link follows whichever host published last. Documented,
  not corrected.
- **Cloned VMs with identical OS ids**: one key, one host entity, as before (002 edge
  cases). A static identity per clone separates them.
- **A static host identity equal to another system's key** on the same template (e.g. an
  inbound endpoint keyed by host name) joins that record, by design (FR-007).
  Auto-discovered host keys are opaque hashes and cannot collide by accident.
- **A key changed or cleared by a user in Omnismith**: the host is adopted again by
  `machine_id` at the next start (FR-012, never overwriting a key it finds). A module
  entity has no adoption, so a new entity is created with the key.
- **Device key changes mid-run** (device swapped): a new entity from then on; the old one
  stops receiving values (US-4/2).
- **Entity template remapped to an existing template with other records**: only the
  record holding the key matches; others are untouched.
- **Reconciliation `off` with the entity template missing**: fails at startup with 001's
  missing-list message, as 003 FR-022.
- **Host link's display attribute**: if the `hostname` module is enabled later, an
  existing reference keeps the display attribute it was created with (FR-004).
- **API unreachable, 401, 403 `stale_project_grant`, 429**: as 003. A 401 still ends the
  run. "Not found" on a module entity is handled by FR-015, not 003 FR-015.
- **Buffered values for a key whose entity never resolves** (the API refuses creation,
  e.g. a quota): the buffer stays within FR-016's bounds, the oldest metric observations
  are dropped, and the failure is logged at every publish attempt.
- **Downgrade to an earlier version**: it ignores keys and finds the host by `machine_id`
  as before, so the same entity is used.

## Out of scope

- Deleting, archiving or marking entities as stale when a device disappears.
- References other than device → host (device → device, host → device, many-to-many).
- Entities not linked to a host, and entities shared by design between hosts.
- Per-filesystem, per-disk and per-interface entities themselves: each is a future module
  spec that uses this feature.
- Removing `machine_id` from the manifest (a breaking slug change, its own ADR).
- Namespacing keys.
- Persisting entity ids across restarts.
- A lifecycle for keys beyond FR-009's cap (forgetting keys not seen for a while).

## Decisions

Taken by the owner before the spec (2026-10-05):
- **Own entities for devices**, rather than device attributes on the host entity. The
  first user is the UPS (012), which should be a record of its own.
- **Generic and minimal**: any module, any number of keyed entities, linked to the host.
  Rejected: a UPS-only shortcut that the disk and network child entities would have to
  rework.
- **The link points from the device to the host** that reports it. Rejected for now: a
  "powered by" link from hosts to the device.
- **The platform's external key identifies every entity omnistat owns**, the host
  included. Rejected: keys for module entities only, which would leave two identity
  mechanisms side by side.
- **`machine_id` stays declared and published**: its slug is a contract (001 FR-002), users
  may filter or automate on it, and it is how existing hosts are adopted.
- **No key prefix**: other systems can share a record on purpose (a UPS card's webhook
  keyed by serial, a future NUT source). Auto-discovered host keys are opaque hashes.

For the owner to confirm with the spec:
- **Lazy resolution** of module entities at the first publish carrying the key (FR-010).
- **Look up first, upsert only when missing** (FR-011), rather than upserting on every
  start: a start that finds its entity writes nothing and fires no "entity updated"
  automation.
- **Adoption never overwrites a key it finds** (FR-012). It warns and repeats at every
  start instead.
- **"Not found" on a device entity recreates it** at the next publish (FR-015). The host's
  loss still ends the run.
- **The host link is rewritten on every dimension update** (FR-018), so a moved device
  follows its host.
- **Display attribute of the host link**: `hostname` if enabled, else the identity
  (FR-003). Never changed once created.
- **At most 256 keys per module per process** (FR-009); keys up to 128 characters
  (FR-008).
- **References may target only the host template** in this feature (FR-002).
- **ADR-0014** (entities are identified by the platform's external key; module-owned
  entities), accepted 2026-10-05. ADR-0004 (identity derivation) is unchanged.

## Open questions

None. The decisions above await the owner's confirmation with the spec. The plan's
spike checks against the local API that a lookup by key sees an upsert's result at once,
as the server source suggests (the key lives on the entity row, under a unique index).
It also checks that an upsert with only the identity attribute fires
`on_entity_created` and nothing else.

## Implementation notes (2026-10-05)

Implemented per `plan.md` (tasks T001–T012). ADR-0014 accepted by the owner, 2026-10-05.
Accepted on the sandbox and e2e results below; the feature ships with 012.

- **Sandbox acceptance (NFR-006), 2026-10-05, local API, project "Omnistat Test":**
  `make sandbox` is all green (`-count=1`):
  - `TestSandbox_Resolve`: a fresh host is created holding its key and found by an
    immediate lookup, which confirms that the key does not lag. A legacy host
    (`machine_id` set, no key) is adopted, not duplicated, then found by key.
  - The pre-011 `sandbox-run` host from 2026-09-24 was adopted by `TestSandbox_Run`. It
    still exists once and now holds the key `sandbox-run`.
  - `TestSandbox_UPS` (with 012): see that spec.
  - The run, CPU, memory, disk and net sandbox tests pass unchanged.
- **e2e-systemd, 2026-10-05:** 275/275 checks on Fedora 44, Debian 12, Ubuntu 24.04,
  Rocky 9 and Rocky 8, including "the same host entity after the upgrade and the reboot"
  under keyed resolution, and the UPS record resolved by key inside the service.
- **Environment note:** the first sandbox run failed every metric read-back, the 0.5.0
  modules' included. The local stack's metric-writer consumer had a backlog of about
  33,000 messages after a Kafka broker outage. Once it drained (about 7 minutes) the
  run's metrics appeared and the rerun passed. Its log also shows 390 "There is already
  an active transaction" failures that it skipped and committed, which is an api-ng
  issue, not omnistat's.
- **SDK bump to v1.0.18** (T002). `NewUpdateEntityRequest()` lost its argument, and the
  discovery templates need `inbound_endpoints`. Both are fixed; no behaviour changed.
- **SDK bug found:** the generated decoder of `attribute_values` (oneOf map | array)
  rejects an empty object, which the API returns for a record with no values. The
  client reads the `id` from the raw body in that case and never projects lookups to
  `fields=id`. Recorded in the API notes, to report upstream.
- **Platform facts from the server source** (T001): lookup by key reads the entity
  row, so it sees an upsert at once; only search lags. Host resolution therefore no
  longer uses `settle`, and the old lag and race tests of 002 were replaced by tests of
  the keyed semantics (eight concurrent first starts → one host).
- **Deviations:**
  - The display attribute of a host link is chosen by a **label rank** that manifests
    declare (`hostname` 2, `machine_id` 1), so the core names no other module's slug
    (FR-003).
  - Dry-run JSON prints one document per entity per publish: the host's, then one per
    module entity with `template`, `key` and `would_create`. `version` stays 1.
  - The daemon's publish summary gains `entities` and `entities_failed`.
  - A module that declares host attributes alongside an entity template is allowed
    (the `gadget` fixture does). Only a module-level template override is refused for it
    (FR-005).
- **Not verified:** which automation events an upsert fires (no automation exists in the
  sandbox project).

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md`
