---
feature: 002-host-identity
status: implemented       # draft | review | approved | implemented | superseded
approved: 2026-09-21
implemented: 2026-09-22
created: 2026-09-21
owners: [evgenii]
supersedes: null
adr: [0001]
depends_on: [001-module-schema-reconciliation]
---

# Feature: `machine-id` module & host identity

## Summary

Every value omnistat publishes belongs to exactly one *host entity* in the project.
This feature defines how omnistat knows *which* entity that is — on the first run and
on every run after — so that restarts, upgrades, IP changes and hostname changes never
produce a second entity for the same machine. The `machine-id` module supplies a
stable identity: autodiscovered from the operating system, overridable with a static
value, and published as an attribute on the host entity. The core uses it to find or
create the host entity idempotently.

## Users & context

- **Operator** — installs omnistat on physical hosts, VMs, cloud instances and
  occasionally containers; some are cloned from images (identical machine ids), some
  are ephemeral. Wants one entity per real machine, forever, and a way to pin identity
  by hand when autodiscovery is wrong.
- **Fleet operator** — hundreds of hosts; wants to be able to tell them apart in
  Omnismith and to reason about "the same host" across reinstalls if they choose to.
- **Security-conscious operator** — knows that the raw OS machine id is considered a
  confidential value on Linux and does not want it exposed in a third-party system.

## User stories

### US-1 — First run creates the host entity once (P1)
As an operator, I want the first run of omnistat to create the host entity, and every
later run to reuse it, so that my project has exactly one record per machine.

**Acceptance scenarios**
1. **Given** a reconciled project with no host entities and a machine with an OS
   machine id, **When** omnistat runs, **Then** exactly one entity is created on the host
   template with the identity attribute set, and its id is used for the rest of the run.
2. **Given** that entity exists, **When** omnistat runs again (after a restart, upgrade,
   hostname change or IP change), **Then** no entity is created and the same entity is used.
3. **Given** that entity exists, **When** two omnistat processes on the same machine
   start at the same time, **Then** still only one entity exists afterwards.

### US-2 — Pin identity by hand (P1)
As an operator, I want to set a static identity so that a cloned VM, a container, or a
machine whose OS id changed after reinstall still maps to the entity I intend.

**Acceptance scenarios**
1. **Given** a static identity in config, **When** omnistat runs, **Then** autodiscovery
   is skipped and the static value is used verbatim as the identity.
2. **Given** a static identity that matches an existing entity's identity attribute,
   **When** omnistat runs, **Then** that entity is reused.
3. **Given** a static identity that is empty or contains only whitespace, **When**
   omnistat starts, **Then** it fails with a configuration error before any network call.

### US-3 — Refuse to guess (P1)
As an operator, I want omnistat to stop rather than invent an identity, so that a
misconfigured host never floods my project with a new entity per run.

**Acceptance scenarios**
1. **Given** no static identity and no discoverable OS machine id (e.g. a minimal
   container), **When** omnistat runs, **Then** it fails before any write, explains what
   it looked for, and tells the operator how to set a static identity.
2. **Given** an identity and *more than one* entity carrying it, **When** omnistat runs,
   **Then** it does not create another entity, uses the oldest one, and warns with the
   ids of all duplicates.

### US-4 — Don't leak the raw machine id (P2)
As a security-conscious operator, I want the published identity to be derived from the
OS machine id rather than the raw value, so that it cannot be used to correlate this
host with other systems that hold the same id.

**Acceptance scenarios**
1. **Given** autodiscovery on Linux, **When** the identity is published, **Then** the
   value is a fixed-length derived token, stable across runs, and not equal to the
   contents of the OS machine id file.
2. **Given** a static identity, **When** it is published, **Then** it is published
   verbatim (the operator chose it).

### US-5 — See the identity without writing (P2)
As an operator, I want to print the identity omnistat would use, its source, and the
entity it resolves to, so that I can verify before the first apply and debug later.

**Acceptance scenarios**
1. **Given** any configuration, **When** I run the identity inspection command, **Then**
   I see the identity value, its source (`static`, `linux-machine-id`, `darwin-platform-uuid`),
   and — if a project is reachable — whether an entity with it exists and which one,
   with nothing written.

## Functional requirements

### Module manifest
- **FR-001** The `machine-id` module MUST declare one attribute: the identity, kind
  `text`, default slug `machine_id`, attached to the host template (spec 001 FR-003).
- **FR-002** The module MUST be always enabled; a config that disables it is a fatal
  configuration error.
- **FR-003** The identity attribute MUST be remappable like any other (spec 001 FR-007),
  so the identity can land in an existing attribute such as a blueprint's own id field.

### Discovery
- **FR-004** On Linux the provider MUST read the OS machine id from the system machine-id
  file, falling back to the D-Bus machine-id file; on macOS it MUST use the platform UUID
  reported by the OS. Values are trimmed; an empty or all-zero value counts as absent.
- **FR-005** Autodiscovery MUST NOT be attempted when a static identity is configured.
- **FR-006** If neither a static identity nor an OS id is available, startup MUST fail
  with an actionable error (US-3/1). omnistat MUST NEVER generate a random identity.
- **FR-007** The published identity for an autodiscovered Linux/macOS id MUST be a
  derived value: a keyed one-way hash of the raw id with an application-specific
  constant key, rendered as lowercase hex, stable across runs and versions (changing
  the derivation is a breaking change requiring an ADR). Publishing the raw id is not
  offered.
- **FR-008** A static identity MUST be published verbatim after trimming, MUST be
  non-empty, and MUST be at most 128 characters.
- **FR-009** The identity MUST be settable via config file and via environment variable;
  the environment variable wins when both are present.

### Entity resolution
- **FR-010** The core MUST resolve the host entity by searching the host template for
  entities whose identity attribute equals the identity value (exact match).
- **FR-011** If exactly one entity matches, it is the host entity.
- **FR-012** If none matches, the core MUST create one entity on the host template with
  the identity attribute set, and MUST then re-search; if the re-search finds more than
  one (concurrent creation), it MUST apply FR-013 rather than fail.
- **FR-013** If more than one entity matches, the core MUST select the one created
  earliest, MUST warn with all matching ids, and MUST NOT create or delete anything.
- **FR-014** Entity creation MUST set only the identity attribute; it MUST NOT write
  other modules' values (that is the publisher's job, feature 003). The host entity has
  no separate display-name attribute: the `hostname` module (feature 003) owns
  `hostname`, which serves as the human-readable label.
- **FR-015** The resolved entity id MUST be held for the remainder of the run and MUST
  NOT be persisted to disk (identity, not entity id, is the durable key).
- **FR-016** Resolution MUST happen after reconciliation (spec 001) and before any
  publishing; in reconciliation mode `off`/`verify` it still runs.

### Inspection
- **FR-017** An inspection command MUST print identity value, source and — when the
  project is reachable — the resolution result, writing nothing (US-5).

### Observability
- **FR-018** Logs MUST record the identity source and the resolved entity id; they MUST
  NOT record the raw OS machine id.

## Non-functional requirements
- **NFR-001** (reliability) Resolution performs at most: one search, one create, one
  re-search; all with deadlines and the retry policy of spec 001 NFR-003.
- **NFR-002** (safety) Resolution never deletes or replaces an entity (constitution IV).
- **NFR-003** (portability) Discovery is a pure function of the OS files/APIs it reads
  and is tested with fakes; unsupported platforms fail with FR-006's message.
- **NFR-004** (privacy) The raw OS id never leaves the process except as the input to
  FR-007's derivation.

## Data & integration contract

Read: search host-template entities filtered by identity attribute equality (needs the
attribute id/slug resolved by spec 001 FR-025); entity creation timestamps for FR-013.
Write: one entity on the host template with `{ identity attribute: value }`.

## Edge cases & failure modes

- Cloned VMs with identical OS ids → they collapse into one entity; the fix is a static
  identity per clone (US-2). The inspection command makes this visible.
- Reinstalled OS → new OS id → new entity, by design; operator pins the old identity
  if they want continuity.
- Container without machine-id → FR-006.
- Identity attribute remapped to an attribute that is not `text` → spec 001 conflict.
- Search returns the entity but a following write returns 404 (deleted mid-run) → fail
  the run; next run recreates.
- 422 on create → report field errors; do not retry with a different payload.

## Out of scope

- Publishing any value other than the identity (feature 003).
- Persisting a generated identity on disk as a fallback (explicitly rejected, FR-006).
- Windows discovery.
- Cloud instance ids (AWS/GCP/Hetzner metadata) as identity sources — a possible future
  provider option, not part of this feature.
- Merging or deduplicating existing entities.

## Decisions taken during review (2026-09-21)

- FR-007: autodiscovered identity is published **derived** (keyed hash), never raw.
- FR-014: no `name` attribute; `hostname` (feature 003) is the human-readable label.
  Revisit only if the Omnismith UI turns out to require a `name` attribute.

## Open questions

None.

## Implementation notes (2026-09-22)

Implemented per `plan.md`; `tasks.md` T001–T010 done. Findings:

- FR-016 (resolve after reconciliation, before publishing) is exercised by the
  `identity` command in dry-run; the writing path runs in feature 003's loop.
  `identity.Resolve` itself is complete and sandbox-tested with writes.
- Derivation pinned by golden vector; ADR-0004.
- The macOS provider runs a fixed `ioreg` command (no cgo); parsing is guarded
  by a fixture test only — not yet run on real macOS hardware.
- Sandbox acceptance (local API, project "Omnistat Test"):
  `schema apply` created `machine_id` bound to `host` (first real attribute
  creation with `template_ids`); `identity` reported the derived id, source and
  "none yet"; a build-tagged test (`make sandbox`) ran the real `Resolve`:
  dry-run → create → reuse → exact match (prefix and superstring do not match,
  confirming `eq` semantics); a duplicate created through the API made
  `identity` warn and pick the oldest; the raw `/etc/machine-id` value appeared
  nowhere in output or logs.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md`
