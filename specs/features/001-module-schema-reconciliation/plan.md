---
feature: 001-module-schema-reconciliation
status: done              # draft | approved | done
approved: 2026-09-22
spec: ./spec.md
created: 2026-09-22
---

# Plan: Module manifests & schema reconciliation

## Constitution check
- [x] Uses the SDK for all API access (II) — only `internal/omni` imports the SDK.
- [x] Every template/attribute lives in exactly one module manifest with default slugs; no hard-coded schema outside manifests (III) — the only constant outside a manifest is the host template slug `host` (spec FR-003), owned by `internal/manifest`.
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV) — the `omni.API` interface exposes no delete/replace method, so the compiler enforces it.
- [x] No secret can reach a commit, a flag, or a log line (IV) — token/project id are env/YAML only; `slog` handlers never receive them.
- [x] Every mutation is covered by dry-run (IV) — `Plan` is computed once and either printed or executed; there is no code path that writes without a plan.
- [x] Every FR/NFR has a test strategy using a fake API (V) — `omnitest` fake server below.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module imports (VI) — table below; `internal/module/*` packages may import only `internal/manifest`.

## Technical context
- Go 1.26, `CGO_ENABLED=0`, targets linux/darwin × amd64/arm64.
- SDK pinned: `github.com/omnismith-sdk/go v1.0.14` (OpenAPI-generated; `NewAPIClient(cfg)`,
  `cfg.HTTPClient` injectable, `GenericOpenAPIError` carries the response body).
- API operations used (operationId → SDK):
  | Operation | SDK call | Used for |
  |-----------|----------|----------|
  | `getProjectSchema` | `SchemaAPI.GetProjectSchema` | FR-012 current state; FR-025 id resolution |
  | `getMyPermissions` | `AuthAPI.GetMyPermissions` | FR-027 pre-flight |
  | `createTemplate` | `TemplatesAPI.CreateTemplate` | *create template* |
  | `createAttribute` (with `template_ids`) | `AttributesAPI.CreateAttribute` | *create attribute* + initial bind |
  | `createAttributeItem` | `AttributesAPI.CreateAttributeItem` | *add list option* (one per option, additive) |
  | `patchAttribute` (`template_ids` = existing ∪ target) | `AttributesAPI.PatchAttribute` | *bind* existing attribute (FR-017) |
- Discovery reports attribute kinds as semantic strings (`string, number, boolean, datetime,
  date, list, metric, …`) — this is the matching key for FR-014/015, no numeric enums needed
  on the read side. Creation uses `attribute_type` (0 dim / 1 metric / 2 list) + `data_type`
  (0 string / 1 number / 2 bool / 3 datetime / 4 date).
- Not available: a transactional or additive "add attribute to template" call; a server-side
  "create if absent". Both are handled client-side (FR-022 pre-flight, FR-024 re-read).

## Approach

1. **Manifests are Go values, not files.** Each module exposes `func Manifest() manifest.Manifest`.
   A build-time registry (`internal/module.Registry`) lists all modules; unknown names in
   config fail at startup (US-4/2). Manifest validation (FR-001…005) is a pure function
   `manifest.Validate([]Manifest) error` that reports *all* problems at once.
2. **Config is YAML → struct → validated overrides.** `internal/config` loads
   `omnistat.yaml`, applies env overrides for secrets/ids, and produces a `manifest.Overrides`
   value. Override precedence (FR-007) is resolved in `manifest.Resolve(manifests, overrides)`
   which returns the **desired state**: `manifest.Desired{Templates, Attributes}` (each attribute
   lists the template slugs it binds to) keyed by final slug — it lives in `manifest` so that
   `schema` can import it without a cycle. Collisions after overrides (FR-009) are reported here, before any network.
3. **Reconciliation is a pure diff over two plain value types.** `internal/schema` owns
   `Desired`, `Current`, `Plan` and `Diff(desired, current) (Plan, []Conflict)`. `Current` is
   built from the discovery response by `internal/omni`. `Plan` is an ordered slice of four
   action variants (FR-013), sorted per FR-019. No I/O in this package (NFR-005).
4. **Execution is a thin loop over the plan** in `schema.Apply(ctx, api, plan)`: pre-flight
   refuses on conflicts (FR-022); each action calls exactly one `omni.API` method; a
   `omni.ErrAlreadyExists` triggers one `ReadSchema` and a re-diff of the *remaining* actions
   (FR-024); any other error stops with a partial report (FR-026). Result carries the
   final `schema.Resolved` (slug → id maps for templates and attributes, FR-025).
5. **The SDK is wrapped once.** The `API` interface is declared by its consumer, `schema`
   (Go idiom), and implemented by `internal/omni`, which constructs the SDK client, injects an
   `http.Client` whose transport adds deadline, retry with jittered backoff on 429/5xx/timeouts
   (NFR-003), and a `User-Agent: omnistat/<version>`. It maps SDK responses/errors into domain
   types and sentinel errors (`ErrAlreadyExists`, `ErrForbidden`, `ErrNoProject`,
   `ErrValidation{Fields}`) so nothing above it knows the SDK exists. The `API` interface is
   deliberately narrow — read schema, permissions, create template, create attribute, add
   option, bind attribute — and has no destructive method.
6. **Fake API for tests** in `internal/omni/omnitest`: an `httptest.Server` with an in-memory
   schema store implementing the six endpoints with the real JSON shapes, plus fault injection
   (`FailNextWith(status)`, `RaceOnCreate(slug)` that pre-creates the object between the
   client's read and write). Tests drive the *real* SDK against it, so SDK usage is exercised.
7. **CLI** in `internal/cli` with stdlib `flag` and a small subcommand table:
   `omnistat schema plan [--json]`, `omnistat schema apply`, `omnistat schema verify`,
   `omnistat version`. Global flags: `--config`, `--log-level`, `--log-format text|json`.
   Exit codes for `plan` (FR-021): `0` no changes, `2` changes pending, `1` conflict/error.
   Reconciliation *mode* (FR-010) lives in config (`schema.mode: apply|verify|off`) and is
   what the long-running command (feature 003) honours; the explicit subcommands ignore it.

## Package layout (delta)

| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/manifest` | `Manifest`, `Attribute`, `Kind` types; `Validate`; `Overrides`; `Resolve` → `Desired`; host template constant | FR-001…011 |
| `internal/module` | `Module` interface (`Name()`, `Manifest()`), `Registry`, enable/disable | FR-006, US-4 |
| `internal/schema` | `Current`, `Plan`, `Conflict`, `Diff`, `API` interface, `Apply`, `Resolved`; text + JSON renderers | FR-012…026 |
| `internal/omni` | SDK construction, retry transport, `API` interface + implementation, error mapping, `Current` builder | II, FR-012, FR-024, FR-027, NFR-002/003 |
| `internal/omni/omnitest` | In-memory fake Omnismith API (`httptest`) with fault injection | V, NFR-005, FR-024 tests |
| `internal/config` | YAML loading, env, defaults, validation → `Overrides`, `Settings` | FR-006…010, IV |
| `internal/cli` | Subcommands, flags, exit codes, output; wires everything | FR-020, FR-021, FR-028 |
| `cmd/omnistat` | `main()` calls `cli.Run(os.Args, …)` and exits | — |

No `internal/module/<name>` packages in this feature; the tests use a fixture module
in `internal/module/moduletest` (two manifests sharing the host template, one `list`).

Dependency graph (imports point down; modules may only import `manifest`):

```
cmd/omnistat → cli → {config, module, manifest, schema, omni}
                          schema → manifest
                          omni   → schema, manifest, SDK
                          module → manifest
```

## Data flow

`plan`/`apply`/`verify`:
1. `config.Load` (file + env) → `Settings`, `Overrides`; fatal on validation error.
2. `module.Registry.Enabled(settings)` → `[]Manifest`; `manifest.Validate` → fatal on error.
3. `manifest.Resolve(manifests, overrides)` → `schema.Desired`; fatal on slug collision.
4. `omni.New(settings)`; `api.ReadSchema(ctx)` → `schema.Current` (one call, NFR-002).
5. `schema.Diff(desired, current)` → `Plan`, `[]Conflict`.
6. `plan`: render (text or JSON `{version:1, actions:[…], conflicts:[…]}`), exit 0/2/1.
   `verify`: exit 0 if plan empty and no conflicts, else print missing list, exit 1.
   `apply`: if conflicts → print, exit 1 (FR-022). Optional `api.MyPermissions` pre-flight
   (FR-027; skipped if the endpoint errors — best effort). Then `schema.Apply`:
   for each action in order → API call → on `ErrAlreadyExists` re-read + re-diff remaining;
   on other error stop, print done/not-done, exit 1. On success print summary; return `Resolved`.

Idempotency key everywhere is the final slug; ids are never persisted.

## Configuration

| Setting | YAML | Env | Default | Requirement |
|---------|------|-----|---------|-------------|
| Access token | — (never in file) | `OMNISMITH_ACCESS_TOKEN` | required | IV |
| Project id | `project_id` | `OMNISMITH_PROJECT_ID` | required | — |
| Base URL | `base_url` | `OMNISMITH_BASE_URL` | `https://api.omnismith.io/v1` | — |
| Reconciliation mode | `schema.mode` | — | `apply` | FR-010 |
| Host template slug | `schema.host_template` | — | `host` | FR-003, FR-007 |
| Module on/off | `modules.<name>.enabled` | — | per module spec | FR-006 |
| Module template | `modules.<name>.template` | — | manifest default | FR-007 |
| Attribute slug/template/name/description | `modules.<name>.attributes.<attr>.{slug,template,name,description}` | — | manifest default | FR-007, FR-008 |
| Request timeout | `http.timeout` | — | `15s` | NFR-003 |
| Retries | `http.retries` | — | `3` | NFR-003 |
| Log level/format | `log.level`, `log.format` | — | `info`, `text` | FR-028 |

Example `omnistat.yaml`:
```yaml
project_id: 01a0c47a-8397-7406-b6e9-ce26508cd58e
schema:
  mode: apply
  host_template: server          # fit an existing template
modules:
  cpu:
    attributes:
      usage: { slug: cpu_usage } # fit an existing attribute
  ip-address:
    attributes:
      address: { slug: ip_address }
```

## Testing strategy

| Requirement | Test type | Where |
|-------------|-----------|-------|
| FR-001…005 manifest validation (all error classes, all-at-once reporting) | unit, table | `internal/manifest/validate_test.go` |
| FR-006…010 config parsing, env precedence, unknown module, mode enum | unit, golden YAML fixtures | `internal/config/*_test.go` |
| FR-007/009 override precedence & collisions | unit | `internal/manifest/resolve_test.go` |
| FR-011 union/merge onto shared template | unit | `internal/manifest/resolve_test.go` |
| FR-013…019 diff: every action type, every conflict class, list-option add, bind-preserves, determinism | unit, table | `internal/schema/diff_test.go` |
| FR-012, FR-025 discovery → `Current`; id resolution | unit via fake server through real SDK | `internal/omni/read_test.go` |
| FR-022…024, FR-026 apply: pre-flight refusal, ordering, race re-read, partial failure | integration via `omnitest` | `internal/schema/apply_test.go` |
| FR-024 concurrent applies converge | integration: two `Apply` goroutines on one fake | `internal/schema/apply_race_test.go` (`-race`) |
| FR-027 permission error mapping | unit via fake 403 | `internal/omni/errors_test.go` |
| NFR-003 retry/backoff/deadline | unit with fake transport | `internal/omni/transport_test.go` |
| FR-020/021 CLI output & exit codes | CLI test via `cli.Run` with fake server URL | `internal/cli/schema_test.go` |
| FR-028 log contents never include token | unit: capture `slog` output | `internal/cli/logging_test.go` |
| NFR-001 | benchmark, not gated | `internal/schema/diff_bench_test.go` |
| Whole US-1/US-2/US-3 flows | golden-output end-to-end via `omnitest` | `internal/cli/e2e_test.go` |

## Risks & unknowns
- **Bind race (FR-017/024):** `PATCH /attributes/{id}` replaces that attribute's template list;
  two hosts binding the same attribute to different templates concurrently could drop one
  binding. Mitigation: bind is read-modify-write from a *fresh* read taken immediately
  before, and `apply` re-reads at the end and re-diffs — a missing binding is redone.
  Documented as a residual risk; the "fit existing schema" path is normally run once.
- **Discovery payload growth:** on very large projects the single schema call is heavy;
  acceptable per NFR-001/002; revisit with pagination if the platform adds it.
- **`getMyPermissions` semantics** (which strings mean "can write schema") are not documented
  in the contract; treated as best-effort pre-flight, real errors still mapped from 403.
- **YAML library:** `github.com/goccy/go-yaml` chosen over `gopkg.in/yaml.v3` (maintenance
  mode) for active upkeep and better error positions; it is the only non-SDK dependency.

## Decisions taken here that deserve an ADR
- ADR-0002: bind existing attributes via `PATCH /attributes/{id}.template_ids` (attribute-side
  replace) rather than `PATCH /templates/{id}` (template-side replace) — smaller race window.
- ADR-0003: the `omni.API` interface intentionally has no destructive methods; the compiler
  is the guard for constitution IV.
