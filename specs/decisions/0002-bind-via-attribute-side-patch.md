---
status: accepted
date: 2026-09-22
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0002: Bind existing attributes with `PATCH /attributes/{id}` (attribute-side), not `PATCH /templates/{id}`

## Context
Spec 001 FR-017 requires binding an existing attribute to a template while
preserving every existing binding. The platform offers no additive "add
attribute to template" call. Both available calls have replace semantics:
`PATCH /templates/{id}` with `attributes`/`attribute_ids` replaces the
**template's whole attribute list**; `PATCH /attributes/{id}` with `template_ids`
replaces **that attribute's template list**. New attributes need neither: they are
bound at creation with `template_ids` on `POST /attributes`.

## Decision
Bind existing attributes through `PATCH /attributes/{id}` with
`template_ids = (templates currently bound, from a fresh read) ∪ {target}`.
After any bind, `Apply` re-reads the schema once and re-diffs, redoing a binding
lost to a concurrent writer.

## Alternatives considered
- **Template-side patch** — a race between two hosts binding *different*
  attributes to the same template would drop one of them (whole list replaced).
  With the attribute-side call, two hosts must race on the *same attribute* to
  interfere, and then they want the same outcome.
- **Ask the platform for an additive endpoint** — worth raising upstream; would
  supersede this ADR. Tracked, not blocking.

## Consequences
- Positive: smallest possible blast radius; new attributes never need a patch.
- Negative: still read-modify-write; the residual race is documented in plan 001
  and mitigated by the post-bind verification read (one extra call, only when a
  bind happened).
