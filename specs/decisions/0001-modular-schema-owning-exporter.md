---
status: accepted
date: 2026-09-21
deciders: [evgenii (founder), Claude (agent)]
supersedes: null
---

# ADR-0001: omnistat is a modular, schema-owning exporter, not a blueprint client

## Context

omnistat is the first app in the `omnismith-apps` organisation and a dogfooding
vehicle for the Go SDK. The initial assumption was to bind it to the featured
*Server Monitoring* marketplace blueprint (templates `team`/`service`/`server`/`incident`,
metrics `cpu_usage`/`memory_usage`/`disk_usage`), because that blueprint is the
obvious showcase for host monitoring.

Reviewing that assumption on 2026-09-21 surfaced its costs:

- The blueprint's schema is a ceiling. Anything a collector can gather that has no
  attribute in the blueprint (load average, network, uptime, CPU model, …) has no home.
- A blueprint version bump or a user's custom schema would break the app, or force a
  parallel "custom mapping" mode anyway.
- Omnismith's differentiator is *runtime schema*. An app that can only fill a fixed
  schema does not demonstrate the platform; an app that *scaffolds* its schema does.

Constraints: constitution II (SDK only), IV (safe/idempotent), VI (simplicity).

## Decision

omnistat is a **modular exporter and schema scaffolder**:

1. **Modules** are the unit of functionality (`machine-id`, `hostname`, `ip-address`,
   `cpu`, `memory`, `disk`, …). Each module consists of:
   - a **manifest** — the attributes it owns (kind, default slug, description, list
     options) and the template it attaches them to (default slug);
   - a **provider** — the code that produces the values for those attributes from the
     host (autodiscovered) or from static configuration.
2. The **core** owns the pipeline: it takes the union of all enabled manifests, applies
   the user's slug/template overrides from config, **reconciles** the resulting schema
   against the Omnismith project, then publishes dimensions and ingests metrics.
   Modules never call the API themselves.
3. **Templates are reconciled once by the core**, from the merged manifests — never by
   individual modules, so several modules attaching to the same template cannot race.
4. **Reconciliation is additive only.** omnistat creates missing templates, attributes
   and list options and binds attributes to templates. It never deletes, renames, retypes
   or unbinds anything that already exists. A conflict (e.g. existing slug with a
   different attribute kind) is an error surfaced to the user, not something to fix
   silently.
5. **Defaults are the contract.** Every manifest ships default slugs. With no
   configuration, the same set of modules produces the same schema in any project and
   across redeployments. Overrides exist to fit an existing schema (including the
   *Server Monitoring* blueprint) and are the user's responsibility.
6. **Identity is a module.** `machine-id` provides the idempotency key for the host
   entity; it is autodiscovered (e.g. `/etc/machine-id`) and overridable with a static
   value in config.

## Alternatives considered

- **Blueprint-bound client (original plan)** — simplest to specify and ship, but caps
  the data model at the blueprint, couples the app to a marketplace artefact the app
  does not control, and shows nothing of the platform's dynamic-schema strength.
- **One monolithic collector with a single big manifest** — fewer moving parts, but no
  way to enable/disable data sources per host, and every new metric touches the same
  code path; contradicts constitution VI in the medium term.
- **Modules that each reconcile their own schema** — the most "self-contained" module
  design, but shared templates would be reconciled N times with racy partial views;
  rejected in favour of core-owned reconciliation (point 3).
- **Full bidirectional schema sync (create *and* remove)** — would keep projects
  pristine, but makes a config typo capable of destroying user data; rejected (point 4).

## Consequences

- Positive: the app works against an empty project *and* an existing one; new data
  sources are additive work (a manifest + a provider + tests); blueprint compatibility
  becomes a shipped example config rather than an architectural dependency; the app
  exercises schema, discovery, entity and metrics parts of the SDK.
- Negative / accepted trade-offs: a schema-reconciliation engine is real work before
  the first metric flows; users who want the blueprint layout must supply a mapping;
  the app needs schema-write permissions in the project, not only entity-write.
- Follow-ups:
  - Constitution III rewritten to reflect this decision (v1.0.0, ratified same day).
  - Spec 001 must define the manifest schema, the config override format, the
    reconciliation algorithm and its conflict rules, and a dry-run/plan mode.
  - Spec 002 must define `machine-id` and host-entity idempotency.
  - Ship `examples/` mapping for the *Server Monitoring* blueprint once the
    override format exists (tracked, not scheduled).
