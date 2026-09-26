Application packages live here, one per concern, created only when a `plan.md`
justifies them against a requirement ID (constitution VI).

| Package | Concern | Introduced by |
|---------|---------|---------------|
| `cli` | Commands, flags, exit codes, wiring of config → modules → API → run loop | 001, 002, 003 |
| `config` | YAML + environment settings, validation | 001 |
| `manifest` | Module schema contracts, overrides, desired schema (pure) | 001 |
| `schema` | Diff of desired vs current schema, additive apply, resolved ids | 001 |
| `module` | `Module` / `Provider` contracts, the registry, and `Omissions` (one omission record per collection) | 001, 003, 005 |
| `module/machineid` | Identity module: `machine_id` attribute, OS discovery, derivation | 002 |
| `module/hostname` | Reference value module: `hostname` dimension | 003 |
| `module/cpu` | First metric provider: usage rate, load averages, CPU dimensions | 004, 005 |
| `module/memory` | Memory used %, available and total; stateless, macOS total-only | 005 |
| `module/disk` | System volume space and inodes; physical-disk I/O rates, busiest disk | 008 |
| `hostread` | Stateless per-area host readers (CPU, memory, disk, with Linux device classification); the only package importing gopsutil (ADR-0009) | 005, 008 |
| `module/moduletest` | Fixture modules (`probe`, `volume`, `ident`: names no real module uses) and scripted providers for tests | 001, 003, 008 |
| `identity` | Find-or-create of the host entity | 002 |
| `collect` | Validation, stamping, bounded buffer, scheduler, one-shot collection, per-platform gating (pure, injected clock) | 003, 004 |
| `publish` | Batches → entity update + metric ingestion; list-id mapping; dry-run printer | 003 |
| `omni` | The only package that imports the SDK; implements the narrow API interfaces | 001, 002, 003, 004, 005 |
| `omni/omnitest` | `httptest` fake of the Omnismith API; `SearchLag`/`SchemaLag` model the platform's asynchronous writes | 001, 002, 003, 005 |
| `settle` | Bounded, injectable wait for a write to become visible to reads (the platform processes writes asynchronously) | 001, 002 (amended) |
