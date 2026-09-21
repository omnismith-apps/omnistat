---
feature: NNN-kebab-slug
status: draft            # draft | approved | done
spec: ./spec.md
created: YYYY-MM-DD
---

# Plan: <feature name>

## Constitution check
- [ ] Uses the SDK for all API access (II)
- [ ] Every template/attribute lives in exactly one module manifest with default slugs; no hard-coded schema outside manifests (III)
- [ ] Reconciliation stays additive; no destructive API call anywhere (III, IV)
- [ ] No secret can reach a commit, a flag, or a log line (IV)
- [ ] Every mutation is covered by dry-run (IV)
- [ ] Every FR/NFR has a test strategy using a fake API (V)
- [ ] Each new package/dependency is justified by a requirement ID; no module-to-module imports (VI)

## Technical context
- Go version, target platforms, runtime constraints.
- SDK version pinned: `github.com/omnismith-sdk/go vX.Y.Z`.
- Relevant API operations (operationId): …

## Approach
Narrative of how the requirements will be met. Reference FR/NFR IDs.

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/…` | … | FR-00x |

## Data flow
Sequence or bullet list: input → transformation → SDK call → outcome. Include
idempotency keys, batching and retry behaviour.

## Configuration
| Setting | Env var | Flag | Default | Requirement |
|---------|---------|------|---------|-------------|

## Testing strategy
| Requirement | Test type | Where |
|-------------|-----------|-------|
| FR-001 | unit | `internal/.../x_test.go` |
| FR-002 | integration (httptest fake of Omnismith API) | … |

## Risks & unknowns
- …

## Decisions taken here that deserve an ADR
- …
