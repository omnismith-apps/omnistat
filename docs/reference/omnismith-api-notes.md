# Reference: Omnismith API & Go SDK notes

Working notes for agents. Verify against the OpenAPI contract (`openapi.yaml` in the
`omnismith-apps` workspace, API v1.0.12) and the SDK source when in doubt.

## Go SDK

- Module: `github.com/omnismith-sdk/go` (latest tag at scaffold time: **v1.0.13**, `go 1.23`).
- OpenAPI-generated, flat package. Import as `omnismithsdk "github.com/omnismith-sdk/go"`.
- Construction:
  ```go
  cfg := omnismithsdk.NewConfiguration()
  cfg.Servers = omnismithsdk.ServerConfigurations{{URL: baseURL}}
  cfg.AddDefaultHeader("Authorization", "Bearer "+token)
  cfg.AddDefaultHeader("X-Omnismith-Project-Id", projectID)
  client := omnismithsdk.NewAPIClient(cfg)
  ```
- Call shape: `client.<Group>API.<Operation>(ctx, pathParams...).<Body>(req).Execute()`
  returning `(model, *http.Response, error)`. Groups include `EntityAPI`, `TemplatesAPI`,
  `AttributesAPI`, `SchemaAPI`, `MarketplaceAPI`, `ProjectsAPI`, `AccessTokensAPI`.
- Generated docs live in the SDK's `docs/` folder — one Markdown file per API group and model.

## Operations most relevant to a schema-reconciling exporter

| operationId | Method & path | Notes |
|-------------|---------------|-------|
| `getProjectSchema` | `GET /discovery/project-schema` | Read current schema; diff against module manifests; resolve slugs → ids. Attributes carry a semantic `type` string (`string, number, boolean, datetime, date, file, image, markdown, list, reference, metric`) and `options`; templates carry bound attribute ids/slugs |
| `createTemplate` | `POST /templates` | Create missing templates (`attribute_ids`/`attribute_slugs` optional) |
| `patchAttribute` | `PATCH /attributes/{id}` | **Bind an existing attribute to a template**: `template_ids` replaces *that attribute's* template list — send existing ∪ new. Preferred over `PATCH /templates/{id}` (which replaces the template's whole attribute list) for a smaller race window |
| `createAttribute` / `setAttributeItems` / `createAttributeItem` | `POST /attributes`, `/attributes/{id}/items` | Create attributes (`attribute_type` 0 dim/1 metric/2 list/3 ref; `data_type` 0 string/1 number/2 bool/3 datetime/4 date; slug `[A-Za-z0-9_]`) and list options |
| `getMyPermissions` | `GET /auth/me/permissions` | Pre-flight: can this token write schema? |
| `searchEntities` | `POST /entities/search/{template_id}` | Find the existing host entity by its identity attribute (idempotency) |
| `createEntity` | `POST /entities/template/{template}` | Publish dimensions |
| `updateEntity` | `PATCH /entities/{id}` | Partial dimension update (`last_check_at`, status) |
| `batchWriteEntities` | `POST /entities/batch` | Bulk create/update (`op` = create/update/replace/delete; `replace` clears omitted attrs) |
| `ingestEntityMetrics` | `POST /entities/{id}/metrics` | Batch `metric_values` (`attribute_slug` or `attribute_id`, `value`); returns **202** — async pipeline |

## Auth & tenancy

- Bearer token: `omni_…` access token (created per user, inherits role + scopes). Env var
  convention in this repo: `OMNISMITH_ACCESS_TOKEN`.
- Every tenant-scoped call must carry `X-Omnismith-Project-Id`. Omitting it yields
  **409 `no_project_selected`**. A **403 `stale_project_grant`** is the only 403 worth
  retrying (refresh credential once).
- `422` responses carry `errors` keyed by `attributes.<slug>` — fix the named field and
  retry once; a second failure is a bug or a rule violation to surface, not to loop on.
- `400` means the payload shape is wrong — re-read the schema, do not retry.

## Value semantics

- Plain scalars for dimensions; `null` clears. Backfill with
  `{ "value": …, "updated_at": "RFC3339 with offset" }`.
- Metric observations are sent as **strings** (`"value": "24.5"`), optional `updated_at`.
- Metric attributes reject `replace`; `create`/`update` append one observation, the
  metrics endpoint is for many observations or explicit timestamps.
