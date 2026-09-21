# Specifications

This project is **spec-driven**: the documents in this folder are the source of
truth and the code is derived from them. If code and spec disagree, the spec is
either wrong (fix it first) or the code is (fix the code). Never let them drift.

## Layout

```
specs/
├── constitution.md        # Non-negotiable principles every spec/plan must honour
├── features/              # One folder per feature, numbered in delivery order
│   └── NNN-kebab-slug/
│       ├── spec.md        # WHAT & WHY — persistent, evolves with the product
│       ├── plan.md        # HOW — technical approach for this iteration (volatile)
│       ├── tasks.md       # Ordered, context-window-sized work items (ephemeral)
│       └── research.md    # Optional: findings that informed the plan
├── decisions/             # Architecture Decision Records (ADRs)
└── templates/             # Copy these to start a new spec / plan / tasks / ADR
```

## Lifecycle

| Stage         | Artifact               | Owner            | Gate                                              |
|---------------|------------------------|------------------|---------------------------------------------------|
| 0. Constitute | `constitution.md`      | Human            | Agreed once; amended by ADR                       |
| 1. Specify    | `spec.md`              | Human + agent    | Human approves; no `[NEEDS CLARIFICATION]` left   |
| 2. Plan       | `plan.md`              | Agent, human OK  | Passes the constitution checklist                 |
| 3. Tasks      | `tasks.md`             | Agent            | Each task has acceptance criteria and a test      |
| 4. Implement  | code + tests           | Agent            | `make all` green; acceptance scenarios pass       |
| 5. Sync       | `spec.md` updated      | Agent + human    | Spec reflects what was actually built             |

Specs use `status:` frontmatter: `draft → review → approved → implemented → superseded`.
Plans and tasks are disposable after a feature ships; the spec and ADRs are not.

## Rules of thumb

- Specs state **what** and **why**, with acceptance scenarios (Given/When/Then).
  They must not name Go packages, function signatures or libraries — that's `plan.md`.
- Keep a spec small enough to read in one sitting. Split rather than grow.
- Mark unknowns explicitly as `[NEEDS CLARIFICATION: question]` instead of guessing.
- Every requirement gets an ID (`FR-001`, `NFR-001`) so tasks, tests and commits can cite it.
- A decision with lasting consequences (protocol, storage, auth model) gets an ADR,
  even if it was made inside a plan.

Run `make specs-check` to validate structure and frontmatter.
