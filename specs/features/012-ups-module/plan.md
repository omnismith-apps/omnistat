---
feature: 012-ups-module
status: approved         # draft | approved | done
approved: 2026-10-05
spec: ./spec.md
created: 2026-10-05
depends_on: [011-module-entities]
---

# Plan: `ups` module — UPS power state, battery and load from apcupsd

## Constitution check
- [x] Uses the SDK for all API access (II). The module never touches the API. Its
  entity is resolved and published by the 011 core.
- [x] Every template/attribute lives in exactly one module manifest with default slugs;
  no hard-coded schema outside manifests (III). The template `ups` and its 22 attributes
  are declared only in `internal/module/ups`. The provider returns keys. No fixture uses
  the name `ups` or a `ups_` slug (checked).
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV).
  Unchanged.
- [x] No secret can reach a commit, a flag, or a log line (IV). NIS carries no secret;
  serials are not secret (FR-008). Status values are logged at debug only.
- [x] Every mutation is covered by dry-run (IV). The module adds no mutation; its values
  go through 011's per-entity dry-run.
- [x] Every FR/NFR has a test strategy using a fake API (V). The NIS server is faked by
  an in-process TCP listener, and the reader is injectable. See below.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI). One new package, `internal/module/ups` (FR-001). No new dependency: the
  NIS client is ~120 lines of standard library, instead of the unmaintained
  `mdlayher/apcupsd` (owner decision).

## Technical context

apcupsd 3.14.14 source (`src/lib/apcstatus.c`, `src/apcnis.c`, `src/lib/apclibnis.c`,
`src/lib/apclog.c`, `include/defines.h`), read 2026-10-05. This was T001: the throwaway
spike of the spec's two open facts.
- **Framing**: every message is a 2-byte big-endian length followed by that many bytes.
  The client sends `status` (length 6). The server answers with one message per line and
  ends with a zero-length message. Lines are shorter than 256 bytes (`MAXSTRING`). The
  server may instead answer `Apcupsd internal error\n` or `Invalid command\n`.
- **Lines**: `KEY<padding>: VALUE\n` (`STARTTIME:` has no padding; `END APC  :` has a
  space in the key). Split at the first colon and trim both sides.
- **`SELFTEST`** codes: `NO`, `NG` (failed, or failed on load), `WN`, `IP`, `OK`, `BT`,
  `??`, or no line at all when the UPS does not report it. **FR-002's options are
  complete.**
- **`STATUS` text is unreliable for flags**: `COMMLOST` and `SHUTTING DOWN` *replace*
  the whole string. On a UPS on battery during a shutdown, the text shows no `ONBATT`.
  **`STATFLAG : 0x%08X`** is always written and carries every bit: online 0x08, onbatt
  0x10, overload 0x20, battlow 0x40, replacebatt 0x80, commlost 0x100, shutdown 0x200.
  The flags therefore come from `STATFLAG` (FR-014). During lost communication the other
  bits and every reading are the last known ones, so FR-014's suppression stands.
- **Readings** are written only when the UPS reports them (`UPS_Cap`), with fixed units:
  `LINEV/OUTPUTV/BATTV %.1f Volts`, `LOADPCT/BCHARGE %.1f Percent`,
  `TIMELEFT %.1f Minutes`, `ITEMP %.1f C`, `LINEFREQ %.1f Hz`, `NOMPOWER %d Watts`.
- **`XONBATT`** is written only when `NUMXFERS > 0` (no `N/A`). An absent `XONBATT` is
  normal and is not a "field the UPS does not report". `LASTXFER` reads "No transfers
  since turnon" when there has been none.
- **Dates**: `%Y-%m-%d %H:%M:%S %z` plus two trailing spaces. **`BATTDATE`**: USB and
  modbus drivers write `YYYY-MM-DD`; apcsmart and net write the UPS's own text,
  conventionally `MM/DD/YY`. Both are accepted; anything else is an omission.
- `UPSNAME` is written only when non-empty; the USB driver fills it from the device name.

## Approach

### 1. Core additions (owned by this spec)
- **Module settings (FR-005).** `config` keeps any `modules.<name>` key other than
  `enabled`, `interval`, `template` and `attributes` as a module setting. It must be a
  scalar; anything else is a config error, which keeps strictness for nested typos. A new
  optional interface `module.Configurable { Settings() []string; Configure(map[string]string) error }`
  lets a module accept settings. `cli` validates every registered module's settings,
  enabled or not, before any network call: an unknown key, or a key on a module that
  takes none, is a fatal error naming the key. Settings for an unregistered module are
  already an "unknown module" error.
- **Failure logging (FR-018, amends 003 FR-010).** `collect` keeps a per-source
  `failureLog`: the first failure with a given reason is logged at warn; repeats are
  logged at debug and counted; the first success after failures is logged at info
  (`collection recovered`, with `after_failures`). `Scheduler.TakeFailureCounts()` feeds
  the daemon's publish summary (`collection_failures`). `Once` is unchanged in effect: it
  has a single collection.
- **Provider network access (ADR-0015, amends 003 FR-004 and ADR-0005).** No code
  change in the core. The rule is enforced by review and by the module's own tests: one
  connection per call, closed on return, deadline-bound.

### 2. `internal/module/ups`
- `nis.go`: `dialStatus(ctx, addr, limit) ([]line, error)`. It dials with a deadline of
  min(ctx deadline, 5s), writes the `status` frame and reads frames until the zero
  length. It caps each frame at 1 KiB and the reply at 512 lines / 64 KiB (FR-019). It
  closes the connection on every path. A reply line without a colon (the server's error
  texts) fails the collection with that text.
- `status.go`: pure parsing of the line list into typed fields: `number(key, unit)`,
  `flags()`, `datetime(key)`, `date(key)`, `selftest()`. Each returns `(value, present,
  err)`, so absent and invalid stay distinct (FR-010, FR-015).
- `ups.go`: the module.
  - **Manifest:** entity template `ups` ("UPS"); host link `host` → `ups_host`
    (reference, label rank 0); the 21 other attributes of FR-001, all with
    `Platforms: linux`; `ups_selftest` options in FR-002's order.
  - **Settings:** `address` (validated `host:port`, port 1–65535) and `identity`
    (`identity.ValidKey`-equivalent rules, local to the module because modules do not
    import `identity`… the 1–128, no-control-character rule is three lines).
  - **Provider:** `Collect` reads through an injectable `Reader` (the real one dials).
    It builds the key (FR-006) and the observations, all targeting
    `Entity{Template: "ups", Key: key}`. Lost communication publishes only `comm_lost`
    and the four identity dimensions (FR-014). Selftest `NO`/`??` publish nothing
    (FR-012). The transfer pair is published only with a valid `XONBATT` (FR-013).
    `load_w` is rounded to whole watts (FR-011).
  - **State:** one set of attribute keys already reported as "not reported by this UPS",
    so FR-015's notice is logged once per process per key set change, and a flag for the
    first-success info line (FR-022). Both live in the provider, as ADR-0006 allows
    retained state.
  - **Failures:** dial or read error → `fmt.Errorf("apcupsd at %s: %w", addr, err)`; no
    identity → an error naming `modules.ups.identity`; nothing produced at all →
    `Omissions.Err`. Invalid fields → one `Omissions` record (FR-016).
- Default interval 10s (FR-004). Registered `DisabledByDefault` (FR-003).

### 3. Wiring and docs
- `cmd/omnistat/registry()` registers `ups.New()` disabled by default.
- The startup schedule log adds `address` for `ups`, via an optional
  `module.Describer { Describe() []any }` that `cli` appends to the "module scheduled"
  record (FR-021).
- `README.md` (module table, settings, apcupsd `NETSERVER/NISIP/NISPORT`, clear-text
  note NFR-004), the starter config (commented `ups` block), `CHANGELOG.md`,
  `internal/README.md`.

### 4. Acceptance (NFR-006)
- `cli/sandbox_ups_test.go` (build tag `sandbox`): a fake NIS server in the test, the
  `ups` module enabled, one-shot run twice. It checks that the template and reference
  exist, one UPS entity holds the key, `ups_host` points to the host, and the
  dimensions and metrics read back. The second run creates nothing.
- `make e2e-systemd`: a static fake NIS binary (`scripts/e2e-nis`) runs on the
  container's loopback. The service with `ups` enabled logs a collected UPS with no
  failure, which proves the sandbox lets the module dial loopback (NFR-003).
- Owner acceptance on the UPS host (spec NFR-006).

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/module/ups` | NIS reader, status parser, manifest and provider | FR-001…FR-022 |
| `config` | module settings | FR-005 |
| `module` | `Configurable`, `Describer` | FR-005, FR-021 |
| `collect` | per-source failure logging, counts | FR-018 |
| `scripts/e2e-nis` | fake NIS server for the container e2e | NFR-006 |

## Data flow
Tick (10s) → `Collect(ctx)` → dial `address` → `status` → frames → lines → typed fields →
observations targeting `ups/<key>` → `collect` routes them to the `ups/<key>` buffer →
publish: `Keyed.Resolve(ups, key)` → dimension update (+ `ups_host`) and metric ingestion.

## Configuration
| Setting | Env var | Flag | Default | Requirement |
|---------|---------|------|---------|-------------|
| `modules.ups.enabled` | — | — | `false` | FR-003 |
| `modules.ups.interval` | — | — | `10s` | FR-004 |
| `modules.ups.address` | — | — | `127.0.0.1:3551` | FR-005 |
| `modules.ups.identity` | — | — | (serial) | FR-005, FR-006 |

## Testing strategy
| Requirement | Test type | Where |
|-------------|-----------|-------|
| FR-001…FR-004 | unit (manifest, defaults, registry default off) | `module/ups/ups_test.go`, `cmd/omnistat` |
| FR-005 | unit (config keeps settings; cli rejects unknown keys; address/identity validation) | `config/config_test.go`, `cli/cli_test.go`, `module/ups/ups_test.go` |
| FR-006…FR-015 | unit with recorded status replies (USB Back-UPS, Smart-UPS, on battery, comm lost, shutting down, no serial, wrong unit, every self-test code) | `module/ups/status_test.go`, `module/ups/ups_test.go` |
| FR-016, FR-017, FR-019 | fake NIS server: refused, hung (deadline), oversized frame, error text, early close | `module/ups/nis_test.go` |
| FR-018 | unit (first, repeat, change of reason, recovery, counts) | `collect/scheduler_test.go`, `cli/run_daemon_test.go` |
| FR-020 | unit (darwin/windows skip) | `module/ups/ups_test.go` |
| FR-021, FR-022 | log capture | `cli/run_test.go`, `module/ups/ups_test.go` |
| NFR-005 | everything above runs with no UPS and no apcupsd | — |
| NFR-006 | sandbox + e2e-systemd + owner | `cli/sandbox_ups_test.go`, `scripts/e2e-systemd.sh` |

## Risks & unknowns
- **Real replies vary by model.** The fixtures are written from the source code and the
  documented `apcaccess` output, not from the owner's UPS. Owner acceptance compares
  against `apcaccess status`.
- **`ONBATTERYDELAY`** may delay the flag. Recorded at acceptance.
- **The local API was down during planning.** The sandbox and e2e runs happen when it
  is up again.

## Decisions taken here that deserve an ADR
- **ADR-0015** — a provider may query a local information service: one short-lived,
  read-only, deadline-bound request per collection, to an address from the module's own
  settings; never the Omnismith API; no connection outlives a call. Amends 003 FR-004 and
  ADR-0005. The reader lives in the module that owns the protocol, because it is not a
  host reading (ADR-0009 stays about gopsutil).
