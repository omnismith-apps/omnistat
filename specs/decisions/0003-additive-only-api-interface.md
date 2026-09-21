---
status: accepted
date: 2026-09-22
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0003: The API interface the core uses has no destructive methods

## Context
Constitution IV forbids every delete, replace, rename or retype call, in any
mode. A rule in a document is only as strong as code review; the SDK exposes
`DeleteTemplate`, `DeleteAttribute`, `ReplaceEntity`, … next to the calls we use.

## Decision
The core never sees the SDK. It depends on `schema.API` (and, per feature, small
sibling interfaces), declared by the consuming package, whose method set is
exactly the additive operations a spec requires: read schema, create template,
create attribute, add list option, bind attribute. `internal/omni` is the only
package that imports the SDK, and it implements only those methods. Adding a
destructive method to an interface is a constitution amendment, not a refactor.

## Alternatives considered
- **Wrapper that panics on destructive calls** — runtime, not compile time.
- **Lint rule forbidding `Delete*` identifiers** — brittle, tooling-dependent.

## Consequences
- Positive: the compiler enforces constitution IV; tests fake a five-method
  interface instead of the whole SDK; SDK upgrades touch one package.
- Negative: every new platform operation needs an interface change and a fake
  implementation in `omnitest` — a deliberate speed bump.
