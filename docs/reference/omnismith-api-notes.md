# Reference: Omnismith API & Go SDK notes

Working notes for agents. Verify against the OpenAPI contract (`openapi.yaml` in the
`omnismith-apps` workspace, API v1.0.12) and the SDK source when in doubt.

> **Read this first: the platform processes every write asynchronously.** Reads
> (search, entity, discovery, metric series) lag behind acknowledged writes. Take ids
> from write responses, wait boundedly (`internal/settle`) when you must read your own
> write back, and never re-create something because a read did not show it. See
> *Writes are processed asynchronously* below.

## Go SDK

- Module: `github.com/omnismith-sdk/go` (pinned: **v1.0.15**, `go 1.23`).
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
| `getEntityChart` | `GET /entities/{id}/chart` | Read a metric back as a series; see *Reading metrics back* below |
| `getEntity` | `GET /entities/{id}` | Read dimension values back (sandbox acceptance only); see *Reading an entity back* below |

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
- **`updated_at` precision**: RFC 3339 with an explicit offset, fractional seconds up to
  **microseconds**; nanoseconds (Go's default `time.Time` JSON) are rejected with 422.
  omnistat truncates to microseconds in `internal/omni`.
## Metrics: creation and read-back (verified 2026-09-22, feature 004 spike)

- **Creating a metric attribute** needs nothing special: the ordinary `createAttribute`
  path with `attribute_type: 1` works, and the attribute reads back from
  `/discovery/project-schema` as `"type": "metric"`. Confirmed end to end —
  create attribute → ingest → 202 → read back.
- **Reading metrics back**: `GET /entities/{id}/chart`
  - `attribute_ids` — **required**, comma-separated metric attribute UUIDs (not slugs).
  - `start`, `end` — **required**, Unix epoch **seconds**. Passing milliseconds is not an
    error: the call returns `200` with `{"series":[]}`. A silently empty series is the
    symptom of wrong units.
  - `bucket_width` — default **`1 hour`**. Enum: `1 second`, `5 seconds`, `10 seconds`,
    `1 minute`, `5 minutes`, `10 minutes`, `15 minutes`, `30 minutes`, `1 hour`,
    `6 hours`, `12 hours`, `1 day`, `1 week`, `1 month`. At the default, observations
    minutes apart collapse into one point — pass an explicit width to see them.
  - `aggregate_func` — default `avg`; one of `sum`, `avg`, `min`, `max`, `count`,
    `first`, `last`.
  - Response: `{"series":[{"attribute_id": "<uuid>", "data":[{"time": "...", "value": 1.5}]}]}`.
    `time` is RFC 3339 (`2026-09-22T16:46:13+00:00`) and decodes through the SDK as a
    `time.Time`. `value` is a JSON **number**, although ingestion sends strings.

- **Value encoding used by omnistat** (feature 003): every dimension is a backfill object
  `{value, updated_at}` with `value` a string (numbers via `strconv.FormatFloat(v,'f',-1,64)`
  — the SDK's scalar union only has `float32`; dates `YYYY-MM-DD`; datetimes RFC 3339 UTC;
  list values as the **item id**) or a boolean. Metric ingestion is chunked at 1 000
  observations per request (our bound; the platform's is undocumented).

## Host readings: memory (feature 005 spike, 2026-09-24)

Not API facts, but recorded here with the other spike findings so a later module does
not re-derive them. gopsutil v4.26.8, `mem.VirtualMemoryWithContext`:

- **Linux** reads `/proc/meminfo` on every call: `Total` = `MemTotal`, `Available` =
  `MemAvailable` (both × 1024). Verified byte-for-byte on the spike host (Linux 7.2); no goroutine is
  started. `Used` = `Total − Available`. `Free` = `MemFree`, which excludes reclaimable
  cache (1 GB free vs 22 GB available on the spike host), so it is useless for "near swap".
  On kernels < 3.14 (no `MemAvailable`) gopsutil silently **emulates** `Available`, and
  nothing in its result says so.
- **Windows** calls `GlobalMemoryStatusEx` per call, with no state: `Total` = `ullTotalPhys`,
  `Available` = `ullAvailPhys`. `Free` is set equal to `Available`, so it is a copy, not a
  separate reading. `UsedPercent` is the OS's integer `dwMemoryLoad`.
- **macOS** calls `host_statistics` + `hw.memsize` per call (plus gopsutil's dlopen-handle
  cache, as for `cpu`). `Available` = free + inactive pages. That is gopsutil's
  approximation, not an OS-maintained estimate: it ignores compressed and purgeable memory.
- All six `make crosscheck` targets build with `CGO_ENABLED=0`.
- The SDK decodes `GetEntityChart` values as **`float32`** (about 7 significant digits).
  Whole-MiB values are exact up to 2²⁴ MiB (16 TiB). Byte-scale values would not be.

## Host readings: disk (feature 008 spike, 2026-09-26)

gopsutil v4.26.8, `disk.UsageWithContext` and `disk.IOCountersWithContext`.

- **Linux `Usage("/")`** is one `statfs` per call: `Total` = `f_blocks·bsize`, `Used` =
  `(f_blocks − f_bfree)·bsize`, `Free` = `f_bavail·bsize` (available to unprivileged
  users). Byte-for-byte equal to `df -B1 /` on the spike host, and `Used ÷ (Used + Free)`
  equals `df`'s Use% (60.49% → `df` 61%). On **btrfs** `InodesTotal` = `InodesFree` = 0,
  which is what `df -i` shows too (`0 0 0 -`).
- **Linux `IOCounters()`** returns every `/proc/diskstats` line that is not all zeros:
  whole disks **and** their partitions (and `dm-*`, `md*`, `loop*`, `zram*` where they
  exist). On the spike host `nvme1n1p3` carried 99.8% of `nvme1n1`'s bytes: partition
  and disk count the same I/O, so summing all lines double-counts. Classification by
  `/sys/block/<name>` (absent = partition) and `/sys/block/<name>/device` (absent =
  virtual) put both NVMe disks in "disk" and all seven partitions in "partition". Bytes
  are sectors × 512, `IoTime` is field 13 (ms with I/O in flight). The values matched
  `/proc/diskstats` read a moment later.
- **Cost**: the first `Usage` + `IOCounters` took about 0.93ms; 20 repeats took
  0.23–0.41ms. `IOCounters` also reads `/run/udev/data/b<maj>:<min>` per device for the
  serial number and label, which is unused. No goroutine is started (1 before, 1 after).
- **macOS** (read, not run): `Usage` is the same `statfs` code. `IOCounters` matches
  **whole** `IOMedia` objects whose parent is an `IOBlockStorageDriver`, which means
  physical disks. APFS synthesized disks sit under a container scheme instead, so they
  are not returned and nothing is double-counted. `IoTime` is gopsutil's
  `ReadTime + WriteTime`, not an OS counter. IOKit and CoreFoundation are opened once per
  process (`sync.Once`), and a failed open is returned on every later call.
- **Windows** (read, not run): `Usage` is `GetDiskFreeSpaceExW`, with `Free` =
  `TotalNumberOfFreeBytes` (quota-unaware). `IOCounters` walks the **drive letters**,
  keeps `DRIVE_FIXED` ones, opens `\\.\X:` with zero access and issues
  `IOCTL_DISK_PERFORMANCE`. It silently skips volumes that do not support it (counters
  disabled), and fails the **whole call** if any open fails with an error other than
  "not found". `ReadCount`/`WriteCount` are 32-bit; `IdleTime` is dropped and `IoTime`
  is never set.
- All six `make crosscheck` targets build with `CGO_ENABLED=0` (macOS uses purego).
- **Read-back precision (sandbox, 2026-09-26).** `disk_root_available_gib` sent as
  `365.73` reads back through `GetEntityChart` as `365.7300109863281` (float32). A test
  that checks "two decimals" with a fixed tolerance on `v×100` breaks once values reach
  the hundreds. Compare in float32 instead: `float32(v) == float32(round2(v))`.
  Dimension read-backs (`EntityValues`) return the exact string that was sent
  (`"929.92"`).

## Writes are processed asynchronously — never assume read-your-writes

**The platform processes writes asynchronously**, entity creation included. A write is
acknowledged (`201`/`200`/`202`) before search, entity reads, metric series and
discovery reflect it. This comes from the platform owner and matches what we measured.
Measured on the local API (2026-09-24, feature 005): after
`POST /entities/template/{t}`, a search for the new entity's unique value returned
nothing for **about 100–280 ms**.

What this means for any code or test in this repository:

- **Take ids from write responses, not from a follow-up read.** Create template,
  create attribute, create list item (`POST /attributes/{id}/items` → `{id}`) and create
  entity all return the new id. `schema.Apply` records them. A later discovery read that
  lacks them does not drop them (001 FR-025).
- **When you must read back your own write, wait for it, bounded.** Use
  `internal/settle`: read immediately, then back off from 100 ms, for about 3 s at most
  (`settle.Default()`). Treat "not visible yet" as unknown, not as absent. Current users:
  - identity's re-search after creating the host entity (002 FR-012);
  - schema's re-read after a create refused as already existing (001 FR-024).
- **Never re-create something because a read did not show it.** It may simply not be
  processed yet (001 FR-025). Redoing an idempotent write, such as a re-bind, is fine.
- **Tests:** the `omnitest` fake is read-your-writes by default. Set
  `srv.SearchLag` / `srv.SchemaLag` (counted in reads, so it stays deterministic) to test
  any path that reads back its own write. Sandbox tests read back through the
  `eventually` helper in `internal/cli/sandbox_test.go`, never with a single read.
- Metric ingestion was already documented as asynchronous (`202`). Wait for the series
  too; do not expect the last observations of a run to be charted immediately.

## Reading an entity back (verified 2026-09-24, feature 005)

- **`GET /entities/{id}`**: by default `attribute_values` is a **slug → string map**
  (`{"hostname": "fedora"}`). With `verbose=true` it is an **array** of
  `{id, slug, value, custom_value, reference_entity_id}`. Number dimensions come back as
  strings in both shapes (`"16"`, `"31820"`). The SDK decodes both through the
  `EntityResponseAttributeValues` union; `omni.EntityValues` accepts either.
- **`fields` is an array parameter.** The OpenAPI spec declares it
  `type: array, style: form, explode: false`. In OpenAPI that means one parameter with
  comma-separated values, `fields=a,b`, and that is why the generated SDKs
  (openapi-generator, `omnismith-sdk-builder`) serialize it that way. The server
  (`api-ng`, `GetEntity` controller) accepts the documented `fields=a,b`, and also
  `fields[]=a&fields[]=b` for clients that send arrays PHP-style. A **bare repeated**
  `fields=a&fields=b` is not an error, but PHP keeps only the last value, so only `b` is
  returned. Verified live for all three forms. Use the SDK, or one of the two accepted
  forms in a hand-built URL.
- A whole-MiB number dimension (`mem_total_mib = 31820`) and whole-MiB metric values
  read back exactly. Chart values come back through `float32`, so a two-decimal
  percentage reads back as, for example, `54.52000045776367`.
