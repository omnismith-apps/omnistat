---
status: ratified
version: 1.1.0
ratified: 2026-09-21
amended: 2026-09-25
amended_by: [0010]
---

# omnistat Constitution

> The principles below bind every spec, plan and implementation in this repository.
> They are deliberately few. Amend them through an ADR in `specs/decisions/`
> (record it under `amended_by`), never ad hoc.

## Vocabulary

| Term | Meaning |
|------|---------|
| **Module** | The unit of functionality (`machine-id`, `hostname`, `ip-address`, `cpu`, …). A module = one manifest + one provider. Modules never talk to the Omnismith API. |
| **Manifest** | A module's schema contract: the attributes it owns (kind, default slug, description, list options) and the template(s) they attach to (default slug). |
| **Provider** | A module's value source: produces the values for the manifest's attributes, either autodiscovered from the host or taken from static configuration. |
| **Reconciler** | Core component that diffs the merged manifests (after config overrides) against the project schema and creates whatever is missing. |
| **Publisher** | Core component that writes dimensions (create/update entities) and ingests metrics. |
| **Identity** | The idempotency key for the host entity, supplied by the `machine-id` module. |

Omnismith terms (template, attribute, dimension, list, reference, metric, entity)
keep their platform meaning; see `AGENTS.md` §5.

## I. Spec before code

No behaviour is implemented without an approved `spec.md` and a `plan.md` that
passes the checklist below. Exploratory spikes are allowed but are thrown away,
not merged.

## II. Dogfood the official SDK

All communication with the Omnismith API goes through
[`github.com/omnismith-sdk/go`](https://github.com/omnismith-sdk/go), pinned in `go.mod`.
Hand-written HTTP calls to the API are a smell; if the SDK is missing something,
record it (issue upstream + ADR) rather than working around it silently.

## III. Modules own their schema (ADR-0001)

omnistat is a **modular exporter and schema scaffolder**, bound to no marketplace
blueprint or predefined schema.

- Every attribute and template omnistat writes is declared in exactly one module's
  manifest, with a default slug. No slug is hard-coded outside manifests.
- Defaults are the contract: with no configuration, the same set of modules yields
  the same schema in any project and across redeployments. Users may override slugs
  and template bindings in config to fit an existing schema (e.g. a marketplace
  blueprint); the defaults never change to accommodate one.
- The core reconciles templates **once**, from the merged manifests; modules never
  reconcile individually.
- Reconciliation is **additive only**: create missing templates, attributes, list
  options and bindings; never delete, rename, retype or unbind. A conflict (existing
  slug, different kind) is an error for the user, not something to resolve silently.
- Identity is a module: `machine-id` provides the host entity's idempotency key,
  autodiscovered and overridable with a static value.

## IV. Safe by default

- Secrets come from the environment or a git-ignored file — never from flags visible
  in `ps`, never from committed config, never in a log line.
- Idempotent: re-running publishes the same host as the same entity; re-running the
  reconciler against an already-reconciled project is a no-op.
- Metric ingestion is at-least-once with bounded, jittered retry; no call blocks on
  the network without a deadline.
- No destructive API calls — `delete`, `replace`, or any schema removal/retyping —
  ever, in any mode. If a future feature needs one, it requires a constitution
  amendment, not a flag.
- Every mutation has a dry-run: the user can see what would be created or written
  before it happens.

## V. Small, testable, observable

- Standard library first; a dependency must earn its place in a plan against a
  requirement ID.
- Every functional requirement maps to at least one automated test; the Omnismith
  API is faked with `net/http/httptest` in `go test`, never called for real.
- One static binary (`CGO_ENABLED=0`), cross-compiled for `linux/amd64`, `linux/arm64`,
  `darwin/amd64`, `darwin/arm64`, `windows/amd64` and `windows/arm64`. Linux is the
  primary target; a module that cannot support a platform says so in its manifest and
  is skipped there, not stubbed.
- Windows is supported (ADR-0010): every feature specifies its Windows behaviour or
  declares it unsupported. The unit tests run on Windows in CI. A feature that adds or
  changes platform-specific behaviour is accepted on a `windows/amd64` host as well as
  on Linux. `windows/arm64` is built and released but not accepted.
- Logs are structured (`log/slog`), levelled, and never include secrets or full
  request bodies. Health/self-metrics are added only when a spec requires them.

## VI. Simplicity gate

A plan must justify every new package, interface or abstraction against a concrete
requirement ID. Three similar lines beat one premature abstraction. A module is not
allowed to import another module; shared needs go through the core.

## Plan checklist

Every `plan.md` must contain this checklist, ticked:

- [ ] Uses the SDK for all API access (II)
- [ ] Every template/attribute lives in exactly one module manifest with default slugs; no hard-coded schema outside manifests (III)
- [ ] Reconciliation stays additive; no destructive API call anywhere (III, IV)
- [ ] No secret can reach a commit, a flag, or a log line (IV)
- [ ] Every mutation is covered by dry-run (IV)
- [ ] Every FR/NFR has a test strategy using a fake API (V)
- [ ] Each new package/dependency is justified by a requirement ID; no module-to-module imports (VI)
