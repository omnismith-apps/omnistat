Application packages live here, one per concern, created only when a `plan.md`
justifies them against a requirement ID (constitution VI).

| Package | Concern | Introduced by |
|---------|---------|---------------|
| `cli` | Commands, flags, exit codes, wiring of config → modules → API → run loop | 001, 002, 003 |
| `config` | YAML + environment settings, validation | 001 |
| `manifest` | Module schema contracts (entity templates, host links, label ranks), overrides, desired schema (pure) | 001, 011 |
| `schema` | Diff of desired vs current schema (references created last), additive apply, resolved ids | 001, 011 |
| `module` | `Module` / `Provider` contracts, the registry, and `Omissions` (one omission record per collection) | 001, 003, 005 |
| `module/machineid` | Identity module: `machine_id` attribute, OS discovery, derivation | 002 |
| `module/hostname` | Reference value module: `hostname` dimension | 003 |
| `module/cpu` | First metric provider: usage rate, load averages, CPU dimensions | 004, 005 |
| `module/memory` | Memory used %, available and total; stateless, macOS total-only | 005 |
| `module/disk` | System volume space and inodes; physical-disk I/O rates, busiest disk | 008 |
| `module/network` | The `net` module: physical-interface traffic, packet, error and drop rates; TCP/UDP health; connection-tracking use | 010 |
| `hostread` | Stateless per-area host readers (CPU, memory, disk, network, with Linux device and interface classification; Windows IP Helper calls); the only package importing gopsutil (ADR-0009) | 005, 008, 010 |
| `module/moduletest` | Fixture modules (`probe`, `volume`, `ident`, `gadget`: names no real module uses) and scripted providers for tests | 001, 003, 008, 011 |
| `identity` | Entities by external key: the host (with adoption of pre-key hosts) and module-owned entities (`Keyed`) | 002, 011 |
| `collect` | Validation, stamping, bounded buffers per entity, scheduler, one-shot collection, per-platform gating (pure, injected clock) | 003, 004, 011 |
| `publish` | Batches → entity update + metric ingestion, per entity with the host link; list-id mapping; dry-run printer | 003, 011 |
| `omni` | The only package that imports the SDK; implements the narrow API interfaces | 001, 002, 003, 004, 005 |
| `omni/omnitest` | `httptest` fake of the Omnismith API; `SearchLag`/`SchemaLag` model the platform's asynchronous writes; external keys and upsert by key | 001, 002, 003, 005, 011 |
| `settle` | Bounded, injectable wait for a write to become visible to reads (the platform processes writes asynchronously); schema apply only since 011 | 001, 002 (amended), 011 |
