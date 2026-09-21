---
feature: NNN-kebab-slug
status: draft            # draft | in-progress | done
plan: ./plan.md
---

# Tasks: <feature name>

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

## Phase 0 — Setup
- [ ] T001 … (files: …) — verify: `make all`

## Phase 1 — Tests first
- [ ] T010 [P] Write failing test for FR-001 (files: …) — verify: test fails for the right reason

## Phase 2 — Implementation
- [ ] T020 Implement FR-001 (files: …) — verify: T010 passes

## Phase 3 — Integration & polish
- [ ] T030 Wire into `cmd/omnistat` — verify: acceptance scenario US-1/1 manually against a sandbox project
- [ ] T031 Update `spec.md` status → `implemented`; record deviations
- [ ] T032 Update README / CHANGELOG
