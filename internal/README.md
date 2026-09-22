Application packages live here, one per concern, created only when a `plan.md`
justifies them against a requirement ID (constitution VI).

| Package | Concern | Introduced by |
|---------|---------|---------------|
| `cli` | Commands, flags, exit codes, wiring of config → modules → API → run loop | 001, 002, 003 |
| `config` | YAML + environment settings, validation | 001 |
| `manifest` | Module schema contracts, overrides, desired schema (pure) | 001 |
| `schema` | Diff of desired vs current schema, additive apply, resolved ids | 001 |
| `module` | `Module` / `Provider` contracts and the registry | 001, 003 |
| `module/machineid` | Identity module: `machine_id` attribute, OS discovery, derivation | 002 |
| `module/hostname` | Reference value module: `hostname` dimension | 003 |
| `module/moduletest` | Fixture modules and scripted providers for tests | 001, 003 |
| `identity` | Find-or-create of the host entity | 002 |
| `collect` | Validation, stamping, bounded buffer, scheduler, one-shot collection (pure, injected clock) | 003 |
| `publish` | Batches → entity update + metric ingestion; list-id mapping; dry-run printer | 003 |
| `omni` | The only package that imports the SDK; implements the narrow API interfaces | 001, 002, 003 |
| `omni/omnitest` | `httptest` fake of the Omnismith API | 001, 002, 003 |
