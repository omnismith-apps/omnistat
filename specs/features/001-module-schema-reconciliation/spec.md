---
feature: 001-module-schema-reconciliation
status: implemented       # draft | review | approved | implemented | superseded
approved: 2026-09-21
implemented: 2026-09-22
created: 2026-09-21
owners: [evgenii]
supersedes: null
adr: [0001]
---

# Feature: Module manifests & schema reconciliation

## Summary

omnistat must be able to run against **any** Omnismith project — empty or already
populated — and make sure the templates and attributes its enabled modules need
exist before a single value is written. Each module declares what it needs in a
*manifest* with stable default slugs; the operator can remap those slugs to fit an
existing schema; the *reconciler* shows what is missing (dry-run) and creates it
(apply), additively and idempotently. This feature is the foundation every other
feature builds on: without it there is nowhere to publish to.

## Users & context

- **Operator** — installs omnistat on one or many hosts, owns an Omnismith project
  and an access token. Wants a predictable schema, wants to see changes before they
  happen, and wants a fleet of hosts to converge on one schema without stepping on
  each other.
- **Schema owner** — has an existing project schema (own design or a marketplace
  blueprint) and wants omnistat to *fit into it*, not to create a parallel one.
- **Module author** — adds a new data source and must be able to declare its schema
  in one place without touching the core.
- Runs as a CLI on Linux (primary) and macOS; typically at boot/service start, and
  on demand. May run concurrently on many hosts against the same project.

## User stories

### US-1 — First run against an empty project (P1)
As an operator, I want omnistat to create everything its modules need in a fresh
project so that I can go from token to data without designing a schema by hand.

**Acceptance scenarios**
1. **Given** a project with no templates and no attributes, and the default module set
   enabled, **When** I run the schema dry-run, **Then** I see one `host` template to
   create and every enabled module's attributes to create and bind, and nothing is
   written to the project.
2. **Given** the same project, **When** I run the schema apply, **Then** the template
   and all attributes exist with their default slugs, kinds and list options, and the
   command reports exactly what it created.
3. **Given** the project right after that apply, **When** I run apply again, **Then**
   nothing is created and the command reports "no changes".

### US-2 — Fit an existing schema (P1)
As a schema owner, I want to map omnistat's attributes onto my existing template and
slugs so that omnistat fills my schema rather than adding a duplicate one.

**Acceptance scenarios**
1. **Given** a project with template `server` and attributes `cpu_usage` (metric) and
   `ip_address` (text), and a config that maps the `cpu` module's usage attribute to
   `cpu_usage` on template `server` and the `ip-address` module's attribute to
   `ip_address`, **When** I dry-run, **Then** those two attributes show as "exists,
   nothing to do" and only the remaining attributes show as "create and bind to `server`".
2. **Given** the mapping above, **When** the attribute `cpu_usage` exists but is not yet
   bound to template `server`, **Then** the plan shows "bind `cpu_usage` to `server`"
   and apply performs the binding without removing any attribute already bound.

### US-3 — Conflicts are surfaced, never resolved silently (P1)
As a schema owner, I want omnistat to refuse to touch my project when its expectations
clash with what exists, so that no data or schema is ever damaged by a mapping mistake.

**Acceptance scenarios**
1. **Given** an existing attribute `cpu_usage` of kind *text* and a manifest that
   declares `cpu_usage` as a *metric*, **When** I dry-run or apply, **Then** the command
   fails, names the attribute, the expected kind, the actual kind and the module that
   owns it, and writes nothing.
2. **Given** an existing list attribute `environment` with options `Production, Staging`
   and a manifest that declares options `Production, Staging, Development`, **When** I
   apply, **Then** `Development` is added and the existing options are untouched.
3. **Given** any conflict in any module, **When** I apply, **Then** *no* create or bind
   happens for any module (conflicts are detected before the first write).

### US-4 — Enable and disable modules (P2)
As an operator, I want to choose which modules run on a host, so that the schema plan
and the data reflect only what I asked for.

**Acceptance scenarios**
1. **Given** the `cpu` module disabled in config, **When** I dry-run, **Then** no `cpu`
   attributes appear in the plan; attributes it created on a previous run are left as
   they are.
2. **Given** a config that enables a module omnistat does not know, **When** I run any
   command, **Then** it fails at startup naming the unknown module and listing the
   known ones.

### US-5 — Fleet-safe and permission-aware (P2)
As an operator running omnistat on many hosts, I want concurrent first runs to converge
and hosts without schema permissions to still work against an already-reconciled project.

**Acceptance scenarios**
1. **Given** two hosts applying simultaneously against an empty project, **When** both
   finish, **Then** the project contains exactly one `host` template and one attribute
   per manifest entry, and both hosts exit successfully.
2. **Given** a token that can write entities but not schema, and a project already
   reconciled, **When** omnistat runs in *verify* mode, **Then** it confirms the schema
   is complete and proceeds; **When** the schema is incomplete, **Then** it fails
   listing what is missing and which permission would be needed to create it.

## Functional requirements

### Manifests
- **FR-001** Every module MUST declare a manifest containing: the module name; for each
  attribute it owns — a human name, a default slug, a kind (`text`, `number`, `boolean`,
  `date`, `datetime`, `list`, `metric`), a description, and for `list` its ordered
  options; and the default template slug each attribute attaches to.
- **FR-002** Default slugs MUST match `^[a-z][a-z0-9_]*$`, be unique across all modules
  shipped in a release, and be stable across releases (a renamed default is a breaking
  change and requires an ADR).
- **FR-003** The default template slug for host-level attributes MUST be the single,
  release-wide constant `host` (human name "Host"), so that all modules land on one
  template unless remapped.
- **FR-004** Manifests MUST be validated at startup: unknown kinds, duplicate slugs
  across enabled modules, list attributes without options, and empty names are fatal
  errors reported before any network call.
- **FR-005** Reference attributes and file/image/markdown data types are OUT of scope
  for manifests in this feature; a manifest declaring them MUST be rejected by FR-004.

### Configuration & overrides
- **FR-006** The operator MUST be able to enable or disable each module; the shipped
  default set and per-module defaults are defined by each module's own spec.
- **FR-007** The operator MUST be able to override, per attribute, its slug and the
  template slug it binds to; and, per module, the template slug for all its attributes.
  Precedence: attribute override > module override > manifest default.
- **FR-008** The operator MUST be able to override the human name and description that
  are used *when creating* an attribute or template; overrides never modify existing ones.
- **FR-009** Overrides MUST be validated with the same rules as defaults (FR-002 pattern,
  uniqueness after overrides are applied). Two modules mapped to the same slug is a fatal
  configuration error.
- **FR-010** Reconciliation MUST have a mode: `apply` (default — create what is missing),
  `verify` (fail if anything is missing, write nothing), `off` (skip entirely; later
  features that need the schema fail at first use with the same missing-list message).

### Desired state & diff
- **FR-011** The desired schema MUST be the union of all enabled manifests after
  overrides: a set of templates, a set of attributes, and attribute→template bindings.
  Two modules attaching to the same template slug contribute to the same template.
- **FR-012** The current schema MUST be read from the project's consolidated schema
  discovery in one call, not by listing templates and attributes separately.
- **FR-013** The diff MUST produce a plan consisting only of these action types:
  *create template*, *create attribute*, *add list option*, *bind attribute to template*.
  No other action type exists.
- **FR-014** Matching MUST be by slug. An existing attribute matches when its slug
  equals the desired slug (after overrides), regardless of its name or description.
- **FR-015** An existing attribute with the same slug but a different kind, or a
  different data type within the dimension kinds (e.g. text vs number), MUST be reported
  as a conflict. Name and description differences MUST NOT be conflicts.
- **FR-016** An existing list attribute MUST match when it has the same kind; options
  present in the manifest but absent in the project are *add list option* actions.
  Options present in the project but absent from the manifest are ignored. Option
  matching is exact and case-sensitive.
- **FR-017** An existing attribute bound to other templates but not to the desired one
  yields a *bind* action; existing bindings on that template MUST be preserved by the
  bind (the platform's template update replaces the association list, so the plan must
  carry the full resulting list).
- **FR-018** An existing template matches by slug regardless of name, description,
  category or groups; none of those are ever modified.
- **FR-019** The plan MUST be deterministic: same inputs, same order (templates first,
  then attributes, then options, then bindings; alphabetical by slug within each).

### Dry-run & apply
- **FR-020** Dry-run MUST print the plan in a human-readable form and MUST be able to
  emit a machine-readable form (JSON, carrying a `version` field) on request. Dry-run
  MUST perform no writes.
- **FR-021** Dry-run MUST let a script distinguish "no changes", "changes pending" and
  "conflict/error" via the exit status.
- **FR-022** Apply MUST refuse to write anything if the plan contains any conflict
  (pre-flight, all modules).
- **FR-023** Apply MUST execute the plan in FR-019 order and report each action as
  created/bound/added, or failed with the platform's error message.
- **FR-024** If an action fails because another actor created the same object in the
  meantime (fleet race), apply MUST re-read the schema, treat the object as existing,
  and continue; the run MUST still succeed if the resulting schema matches the desired one.
  Because the platform processes writes asynchronously, the object may not be visible
  yet: the re-read MUST be repeated within a short, bounded wait before the object is
  concluded absent, and only then is the original error reported.
  *(Amended 2026-09-24.)*
- **FR-025** After a successful apply (or verify), omnistat MUST hold, for every
  desired attribute and template, the platform identifier resolved from the final
  schema, so that later features never look up slugs again during a run. Identifiers
  returned by this run's own writes (created templates, attributes and list items) are
  authoritative. A later read that does not show them yet MUST NOT drop them. An object
  this run created MUST NOT be created again because a read did not show it yet.
  *(Amended 2026-09-24: platform reads lag writes.)*
- **FR-026** If any action fails for a reason other than FR-024, apply MUST stop, report
  what was completed and what was not, and exit non-zero. Re-running MUST be safe and
  MUST pick up where it left off (idempotency via FR-014).
- **FR-027** When the token lacks permission for schema writes, the error MUST say so in
  plain words and suggest `verify` mode; omnistat SHOULD detect this before the first
  write using the platform's own permission listing.

### Observability
- **FR-028** Every action and every conflict MUST be logged structurally with module,
  slug, template slug and action type; no log line may contain the token or project id.

## Non-functional requirements
- **NFR-001** (performance) Plan for ≤ 200 desired attributes against a project with
  ≤ 2 000 existing attributes completes in < 2 s on a typical VM, excluding network time.
- **NFR-002** (network) Dry-run performs exactly one schema read (plus at most one
  permission read); apply performs one schema read, one write per action, and per
  FR-024 event a bounded number of re-reads spread over a few seconds at most
  (amended 2026-09-24).
- **NFR-003** (reliability) Every API call has a deadline; transient failures (5xx,
  timeouts, 429) are retried with bounded jittered backoff, then surfaced.
- **NFR-004** (safety) No API operation that deletes, replaces, renames or retypes is
  ever invoked (constitution IV).
- **NFR-005** (portability) Manifest validation and planning are pure and platform
  independent; they run identically in `go test` without network.

## Data & integration contract

Read: the project's consolidated schema (templates with bound attributes; attributes
with kind, data type, list options).
Write (additive only): templates (name, slug, description); attributes (name, slug,
kind, data type, description, initial template binding); list options (value, order);
attribute→template association lists (full, preserving existing).
Domain mapping of manifest kinds: `text`/`number`/`boolean`/`date`/`datetime` are
*dimension* attributes with the matching data type; `list` is a *list* attribute;
`metric` is a *metric* attribute with numeric data type.

## Edge cases & failure modes

- Empty project → US-1. Project with an unrelated schema → untouched; ours added.
- Attribute exists on another template only → bind (FR-017), never re-create.
- Template exists with different name/description → matched, untouched (FR-018).
- Slug collides after overrides → fatal config error before network (FR-009).
- API unreachable / 401 → fail fast with a clear message; 403 `stale_project_grant` →
  refresh credential once and retry; 409 `no_project_selected` → configuration error
  (missing project id); 422 on a write → report the platform's field-level errors and stop.
- Concurrent first runs → FR-024.
- Rate limiting (429) → NFR-003.
- Config enables a module unknown to this build → fatal (US-4/2).

## Out of scope

- Publishing values, creating the host entity, ingesting metrics (features 002+).
- Reference attributes, file/image/markdown attributes, template groups/UI layout,
  rules, actions, dashboards, automations.
- Removing or renaming anything; migrating a schema from one default set to another.
- Installing marketplace blueprints.
- A persistent local cache of the resolved schema between runs.

## Decisions taken during review (2026-09-21)

- FR-003: default host template slug is `host`.
- Configuration file format is **YAML** (FR-006–FR-010 refer to "config"; all examples in
  later specs use YAML).
- The JSON plan output (FR-020) is *best effort* until 1.0 — it carries a `version`
  field so it can be stabilised later without a breaking change.

## Open questions

None.

## Implementation notes (2026-09-22)

Implemented per `plan.md`; `tasks.md` T001–T063 done. Deviations and findings:

- `Desired` lives in `internal/manifest` (not `schema`) to avoid an import cycle;
  the `API` interface is declared by `internal/schema` (its consumer) — see ADR-0003.
- Existing attributes are bound via `PATCH /attributes/{id}` (attribute-side) —
  see ADR-0002. After any bind, `Apply` performs one verification read and
  re-diff (one call more than NFR-002's budget, only when a bind happened).
- FR-027 pre-flight: the permission strings are undocumented, so the pre-flight
  only surfaces hard 401/403 from `GET /auth/me/permissions`; real permission
  errors are mapped from the write's 403 with guidance to use `verify` mode.
- Sandbox acceptance (local API, project "Omnistat Test", token via `.env`):
  US-1/1 `schema plan` → `+ template host`, exit 2, nothing written;
  US-1/2 `schema apply` → template created (confirmed via discovery), exit 0;
  US-1/3 `plan`/`apply`/`verify` again → "no changes" / "schema complete", exit 0.
  The real build has no modules yet, so attributes/options/binds were exercised
  only against the fake API; feature 002 re-runs acceptance with real attributes.
- Found and fixed during acceptance: a 403 with code `project_access_denied`
  (wrong project id) was explained as a permission problem; it now points at
  `OMNISMITH_PROJECT_ID`.
- `make run ARGS="schema plan"` sources `./.env` for local development; the
  binary itself never reads `.env` (secrets come from the environment, IV).

## Amendment (2026-09-24): asynchronous platform

The platform processes writes asynchronously: a write is acknowledged before discovery
and search reflect it. Found during feature 005 and confirmed by the platform owner.
FR-024, FR-025 and NFR-002 were amended so that reconciliation never takes "not visible
yet" for "absent":

- A fleet-race re-read waits, within a bounded budget, for the object to appear.
- Ids from this run's write responses, including list item ids, which the create
  response now supplies, survive a lagging read.
- The post-bind verification no longer re-creates objects this run already created.
  Only binds are redone, because re-binding is additive.

The fake API models the lag deterministically (`SchemaLag`), and tests cover each case.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md`
