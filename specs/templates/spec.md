---
feature: NNN-kebab-slug
status: draft            # draft | review | approved | implemented | superseded
created: YYYY-MM-DD
owners: []
supersedes: null
---

# Feature: <name>

## Summary
One paragraph: the problem, who has it, and what changes for them when this ships.

## Users & context
- **Who** uses this and in what situation.
- **Where** it runs (host, container, CI, laptop) and what it can assume about the environment.
- **Platforms**: Linux, macOS, Windows — what differs on each, or which are unsupported and why (ADR-0010).

## User stories
### US-1 — <title> (priority: P1)
As a <role>, I want <capability> so that <outcome>.

**Acceptance scenarios**
1. **Given** … **When** … **Then** …
2. **Given** … **When** … **Then** …

### US-2 — <title> (priority: P2)
…

## Functional requirements
- **FR-001** The app MUST …
- **FR-002** The app MUST …
- **FR-003** The app SHOULD … `[NEEDS CLARIFICATION: …]`

## Non-functional requirements
- **NFR-001** (performance) …
- **NFR-002** (reliability) …
- **NFR-003** (security) …

## Data & integration contract
What is read from / written to Omnismith, in domain terms (templates, attributes,
metrics), not in SDK terms. Name templates/attributes by their default slugs and say
which module owns them.

## Edge cases & failure modes
- What happens when the API is unreachable / returns 401 / 403 `stale_project_grant` / 409 / 422?
- What happens on first run vs. subsequent runs?

## Out of scope
Explicitly list what this feature does **not** do.

## Open questions
- `[NEEDS CLARIFICATION: …]`

## Review checklist
- [ ] No implementation details (packages, libraries, signatures)
- [ ] Every requirement is testable and has an ID
- [ ] Every user story has at least one acceptance scenario
- [ ] No `[NEEDS CLARIFICATION]` markers remain
- [ ] Consistent with `specs/constitution.md`
