---
feature: 012-ups-module
status: approved         # draft | review | approved | implemented | superseded
approved: 2026-10-05
created: 2026-10-05
owners: [evgenii]
supersedes: null
adr: [0005, 0007, 0010, 0014, 0015]
depends_on: [001-module-schema-reconciliation, 002-host-identity, 003-run-loop-publisher, 007-linux-service, 011-module-entities]
---

# Feature: `ups` module — UPS power state, battery and load from apcupsd

## Summary

A host with a UPS on its USB or serial port usually runs **apcupsd**, which talks to the
UPS, shuts the host down before the battery runs out, and runs scripts on events (power
lost, power back, battery needs replacing, communication lost). Those scripts and
apcupsd's own tools are local to the host. Nothing tells an operator elsewhere that the
building is on battery, how many minutes are left, or that the battery failed its
self-test.

This feature adds the `ups` module. It reads apcupsd's status from apcupsd's **Network
Information Server** (NIS, TCP, by default on the same host) and publishes the UPS as
**its own entity** on a `ups` template (spec 011), linked to the host that reads it. The
UPS entity carries the power state as dimensions that automations can react to (on
battery, battery low, replace battery, communication lost, overload, last transfer to
battery, self-test result), its identity (name, model, serial number, battery date),
and the battery, load and line readings as metrics.

## Users & context

- **Operator.** Has a UPS attached to one host by USB. In the owner's case one UPS feeds
  three servers and is managed by one of them. Wants a Telegram or push notification
  when the power fails and when it returns, a warning when the battery needs replacing,
  and a chart of charge, runtime and load. Accepts that a state change reaches Omnismith
  at the next publish (about a minute at default settings), not instantly.
- **Where it runs.** On the host running apcupsd, as the 007 systemd service (unprivileged,
  sandboxed) or in the foreground. apcupsd's NIS listens on `127.0.0.1:3551` (the owner
  has restricted it to loopback). apcupsd keeps running whatever omnistat does.
- **Platforms** (ADR-0010). **Linux only**, by the owner's decision. Windows and macOS are
  declared unsupported (FR-020). The NIS protocol is the same everywhere, so enabling
  them later is a matter of acceptance, not of a new reading source.

## User stories

### US-1 — Know when the power fails and returns (P1)
As an operator, I want the UPS entity to say whether the UPS is running on battery, and to
record every transfer to battery, so that an automation can notify me of an outage and of
the power returning, even for a short dip.

**Acceptance scenarios**
1. **Given** the module enabled on a host whose UPS runs on mains, **When** the daemon has
   published, **Then** the UPS entity exists on the `ups` template, linked to the host, with
   `ups_on_battery` = false.
2. **Given** the mains is unplugged from the UPS, **When** the next collection and publish
   complete, **Then** `ups_on_battery` = true, and `ups_last_transfer_at` and
   `ups_last_transfer_reason` show the time and apcupsd's reason for the transfer.
3. **Given** the mains is plugged back in, **When** the next publish completes, **Then**
   `ups_on_battery` = false.
4. **Given** a power dip shorter than the collection interval, **When** the next publish
   completes, **Then** `ups_on_battery` may never have shown true, but
   `ups_last_transfer_at` has changed to the dip's time.

### US-2 — See how long the battery will last (P1)
As an operator, I want the battery charge, the estimated runtime left and the load as time
series, so that I can chart them and alert before the battery runs out.

**Acceptance scenarios**
1. **Given** the module enabled, **When** the daemon runs for one publish interval, **Then**
   the UPS entity has received `ups_battery_charge_pct`, `ups_runtime_min` and
   `ups_load_pct` observations, each equal to what `apcaccess status` shows at that time.
2. **Given** a UPS whose status reports a nominal power, **When** the module is collected,
   **Then** `ups_load_w` is the load percentage × the nominal power ÷ 100, in whole watts.

### US-3 — Know when the battery must be replaced (P1)
As an operator, I want to be told when the UPS reports that its battery needs replacing,
and what its last self-test found, so that I replace it before an outage proves it dead.

**Acceptance scenarios**
1. **Given** a UPS whose status includes the replace-battery flag, **When** the module is
   published, **Then** `ups_replace_battery` = true.
2. **Given** a self-test that apcupsd reports as failed for battery capacity, **When** the
   module is published, **Then** `ups_selftest` = `failed_capacity`, and it keeps that
   value after apcupsd stops reporting the result (FR-012).

### US-4 — Know when monitoring itself breaks (P1)
As an operator, I want to know when apcupsd has lost the UPS, and when omnistat cannot
reach apcupsd, so that silence is never mistaken for "all is well".

**Acceptance scenarios**
1. **Given** the UPS's USB cable is unplugged, **When** apcupsd reports that it lost
   communication, **Then** `ups_comm_lost` = true and no stale reading is published.
2. **Given** apcupsd is stopped, **When** collections fail, **Then** omnistat logs the
   failure once with its reason, logs a count of further failures in its periodic
   summary rather than one line every collection, and logs at info when apcupsd is
   reachable again.

### US-5 — Opt in, on the right host only (P2)
As a fleet operator, I want the module off unless I enable it, so that hosts without a UPS
get no UPS schema and no log noise.

**Acceptance scenarios**
1. **Given** the default configuration, **When** I dry-run the schema, **Then** no `ups`
   template or attribute appears.
2. **Given** `modules.ups.enabled: true` and no other setting, **When** omnistat starts,
   **Then** it reads apcupsd at `127.0.0.1:3551`.
3. **Given** `modules.ups.address: 127.0.0.1:3552`, **When** omnistat starts, **Then** it
   reads apcupsd there; an invalid address is a fatal configuration error before any
   network call.

### US-6 — Keep one record for a UPS that reports no serial number (P2)
As an operator, I want to pin the UPS's identity in config, so that a UPS that reports
no serial number, or a replacement I want to keep under the same record, still maps to
one entity.

**Acceptance scenarios**
1. **Given** `modules.ups.identity: rack-ups-1`, **When** omnistat publishes, **Then** the
   UPS entity's key is `rack-ups-1` and `ups_serial` still shows the reported serial
   (or nothing).
2. **Given** no static identity and a UPS that reports no serial number, **When** the
   module is collected, **Then** nothing is published for the UPS, and omnistat logs that
   the UPS has no identity and how to set one.

## Functional requirements

### Module manifest
- **FR-001** The `ups` module MUST declare one entity template (011 FR-001), default slug
  `ups`, human name "UPS", with exactly these attributes. The UPS entity's identity is
  its external key (FR-006), not an attribute:

  | Key | Default slug | Kind | Human name | apcupsd source | Meaning |
  |-----|--------------|------|------------|----------------|---------|
  | `host` | `ups_host` | reference → host (**host link**) | Managed by | — | The host whose omnistat reads this UPS (011 FR-017) |
  | `name` | `ups_name` | text | UPS name | `UPSNAME` | The name stored in the UPS or set in apcupsd's config; the record's label |
  | `model` | `ups_model` | text | Model | `MODEL` | The UPS model |
  | `serial` | `ups_serial` | text | Serial number | `SERIALNO` | The serial number the UPS reports |
  | `battery_date` | `ups_battery_date` | date | Battery date | `BATTDATE` | The battery date the UPS reports (replacement or manufacture date, depending on the model) |
  | `on_battery` | `ups_on_battery` | boolean | On battery | `STATFLAG` 0x10 (ONBATT) | The UPS is supplying its load from the battery |
  | `battery_low` | `ups_battery_low` | boolean | Battery low | `STATFLAG` 0x40 (LOWBATT) | The UPS reports its battery as low |
  | `replace_battery` | `ups_replace_battery` | boolean | Replace battery | `STATFLAG` 0x80 (REPLACEBATT) | The UPS reports that its battery needs replacing |
  | `comm_lost` | `ups_comm_lost` | boolean | Communication lost | `STATFLAG` 0x100 (COMMLOST) | apcupsd has lost communication with the UPS |
  | `overload` | `ups_overload` | boolean | Overload | `STATFLAG` 0x20 (OVERLOAD) | The UPS reports its load above its capacity |
  | `last_transfer_at` | `ups_last_transfer_at` | datetime | Last transfer to battery | `XONBATT` | When the UPS last switched to battery |
  | `last_transfer_reason` | `ups_last_transfer_reason` | text | Last transfer reason | `LASTXFER` | apcupsd's reason for the last transfer (e.g. low line voltage, self-test) |
  | `selftest` | `ups_selftest` | list | Self-test result | `SELFTEST` | Result of the most recent self-test (FR-012) |
  | `battery_charge_pct` | `ups_battery_charge_pct` | metric | Battery charge | `BCHARGE` | Battery charge, in percent |
  | `runtime_min` | `ups_runtime_min` | metric | Runtime left | `TIMELEFT` | Runtime on battery at the current load, as the UPS estimates it, in minutes |
  | `load_pct` | `ups_load_pct` | metric | Load | `LOADPCT` | Load, in percent of the UPS's capacity |
  | `load_w` | `ups_load_w` | metric | Load (estimated watts) | `LOADPCT` × `NOMPOWER` | Estimated load in watts (FR-011) |
  | `input_v` | `ups_input_v` | metric | Input voltage | `LINEV` | Line (input) voltage, in volts |
  | `output_v` | `ups_output_v` | metric | Output voltage | `OUTPUTV` | Output voltage, in volts |
  | `battery_v` | `ups_battery_v` | metric | Battery voltage | `BATTV` | Battery voltage, in volts |
  | `temp_c` | `ups_temp_c` | metric | Internal temperature | `ITEMP` | Temperature inside the UPS, in °C |
  | `input_hz` | `ups_input_hz` | metric | Input frequency | `LINEFREQ` | Line frequency, in Hz |

- **FR-002** `ups_selftest` MUST be a list with exactly these options, in this order:
  `passed` (`OK`), `failed_capacity` (`BT`), `failed` (`NG`), `warning` (`WN`),
  `in_progress` (`IP`). The plan's spike MUST confirm this is every code apcupsd 3.14
  emits besides `NO` and `??` (FR-012). If it finds another, the spec is amended before
  implementation.
- **FR-003** The module MUST be **disabled by default** and enabled with
  `modules.ups.enabled: true`. When disabled, neither its schema nor its values are
  produced (001 FR-006). Every attribute and the template MUST be remappable (001
  FR-007/FR-008, 011 FR-005).
- **FR-004** The default collection interval MUST be **10s**, overridable per 003 FR-003.

### Configuration
- **FR-005** The module MUST accept two settings of its own, in the config file under
  `modules.ups`:
  - `address`: `host:port` of apcupsd's NIS, default `127.0.0.1:3551`. A host name, an
    IPv4 address or a bracketed IPv6 address, and a port from 1 to 65535. Anything else
    is a fatal configuration error before any network call;
  - `identity`: a static UPS identity (FR-006), trimmed, 1–128 characters.
  Config keys a module does not declare remain errors (the config file is strict). This
  is the first module setting beyond `enabled`, `interval`, `template` and `attributes`.
  The README and the starter config (007) document both settings, commented out.

### Identity
- **FR-006** The UPS entity's external key (011 FR-007) MUST be the static `identity` when
  configured, otherwise the `SERIALNO` the status reports, trimmed and unprefixed. Another
  source keyed by the same serial on the same template (an inbound endpoint fed by a UPS
  network card, a future NUT source) therefore writes to the same record. With neither, the collection
  MUST fail with a reason naming `modules.ups.identity` (US-6/2), and nothing is
  published for the UPS.
- **FR-007** `ups_serial` MUST always be the reported serial number, whether or not a static
  identity is set. With a static identity and no reported serial, it is not published.
- **FR-008** The serial number is not sensitive. It MUST be logged as reported (011 FR-021).

### Reading apcupsd
- **FR-009** Each collection MUST make one request for apcupsd's status to the configured
  NIS address, on a connection opened for that collection and closed before it returns.
  The whole exchange MUST finish within the collection's deadline and at most **5s**.
  Nothing else is sent to apcupsd: omnistat never asks for its event log and never
  sends a command that changes the UPS or apcupsd.
- **FR-010** Each value MUST be taken from its status field independently (FR-001
  "apcupsd source"). A numeric field MUST carry the unit apcupsd documents for it
  (`Percent`, `Minutes`, `Volts`, `C`, `Hz`, `Watts`). A value with another unit or one
  that does not parse is an omission (FR-016), never converted or guessed. Numbers are
  published as apcupsd reports them, without further rounding.
- **FR-011** `ups_load_w` MUST be `LOADPCT × NOMPOWER ÷ 100`, rounded to whole watts, and
  published only when both fields are reported and valid. Its description MUST say it is
  an estimate derived from the nominal power, not a measurement.
- **FR-012** `ups_selftest` MUST be published from the `SELFTEST` code per FR-002. `NO` (no
  self-test result in the last few minutes) and `??` (unknown) MUST publish **nothing**,
  so the entity keeps the last result it was given. Any other code is an omission
  naming the code.
- **FR-013** `ups_last_transfer_at` and `ups_last_transfer_reason` MUST be published
  together, and only when `XONBATT` is a valid time. When apcupsd reports no transfer
  since it started (`N/A`), neither is published, so the entity keeps the previous
  transfer. The time is published as the instant apcupsd gives, with its offset.
- **FR-014** The five booleans MUST come from the status flags of the same status:
  - when apcupsd reports **lost communication**, `ups_comm_lost` = true, and **no other**
    flag or metric from that status is published, because they are stale. The identity
    dimensions (`ups_name`, `ups_model`, `ups_serial`, `ups_battery_date`) still are;
  - otherwise `ups_comm_lost` = false, and each other boolean is true when its flag is
    present and false when it is absent.
- **FR-015** A status field the UPS does not report at all (many USB models report no
  output voltage, temperature or line frequency) MUST be treated as a **steady state**.
  It is logged **once per process** at info, naming the attributes the UPS does not
  report, and is not an omission. If the field appears later, it is published from
  then on.

### Partial collection & failures
- **FR-016** Omitted values (a field reported but invalid, FR-010, FR-012) MUST be reported
  in **one** record per collection, naming each attribute key and the reason (005
  FR-013). The collection fails only when no observation could be produced.
- **FR-017** apcupsd unreachable (connection refused, timeout, closed early, malformed
  frame) MUST be a collection failure (003 FR-010) whose reason names the address and
  the cause. No value is published and the UPS entity is left as it is.
- **FR-018** *(Amends 003 FR-010, all modules.)* Consecutive failures of one module with the
  same reason MUST be logged at the **first** occurrence only (warning). Further failures
  with that reason are counted and reported in the daemon's summary (003 FR-026a). The
  first success after failures MUST be logged at info with the number of failures. A
  different reason is logged as a new first occurrence. A one-shot `run` logs its single
  failure as before.
- **FR-019** A malformed or oversized reply from apcupsd MUST fail that collection and MUST
  NOT crash the process or leave the connection open.

### Platform support (ADR-0007)
- **FR-020** Every attribute MUST be declared collectable on **Linux only**. On Windows and
  macOS an enabled `ups` module is never collected, one startup record says so, and its
  schema is still declared (ADR-0007), so a mixed fleet converges on one schema.

### Observability
- **FR-021** The startup schedule log (003 FR-027) MUST list `ups` with its interval and its
  NIS address. Collected values are logged at debug only (003 FR-026).
- **FR-022** The first successful collection MUST log at info the UPS's model, serial
  number and the identity in use (reported serial or static).

## Non-functional requirements
- **NFR-001** (performance) One collection is one short TCP exchange with a local daemon:
  well under 50ms when apcupsd is healthy, never longer than FR-009's bound.
- **NFR-002** (resources) No connection, goroutine or buffer outlives a collection. At 10s
  collection and 60s publish the module adds at most 54 metric observations and one
  module entity per publish (011 NFR-001).
- **NFR-003** (safety) Read-only towards apcupsd and the UPS (FR-009). No elevated
  privilege, no external command (`apcaccess` is not used), no file read. It MUST work
  as the 007 Linux service without widening its sandbox: loopback TCP is already
  permitted there.
- **NFR-004** (security) The NIS protocol is unauthenticated and unencrypted. The default
  address is loopback. A non-loopback address is allowed, and the README states that the
  status then crosses the network in clear text.
- **NFR-005** (testability) The status reading MUST come through an injectable source, and
  a fake NIS server MUST serve recorded and synthetic status replies. FR-006…FR-019 are
  then testable in `go test` with no UPS and no apcupsd, including lost
  communication, missing fields, wrong units, every self-test code, a refused connection,
  a hung server and an oversized frame.
- **NFR-006** (acceptance) Sandbox acceptance (local API): the schema applied, the UPS
  entity created once and linked to the host, dimensions and metrics read back, all
  from a fake NIS server, and the entity reused after a restart (011 NFR-006).
  `make e2e-systemd` proves the module reads a NIS server on loopback from inside the
  007 sandbox. **Owner acceptance** on the host with the UPS, as the 007 service: values
  against `apcaccess status`; pulling the mains (on battery, last transfer, back on
  mains); unplugging the USB cable (communication lost); stopping apcupsd (one failure
  line, then recovery). Windows and macOS are not accepted (FR-020).

## Data & integration contract

Read: from apcupsd's NIS, the status record (`KEY : VALUE` lines). Fields used: `UPSNAME`,
`MODEL`, `SERIALNO`, `BATTDATE`, `STATUS` (and/or its numeric flag form), `XONBATT`,
`LASTXFER`, `SELFTEST`, `BCHARGE`, `TIMELEFT`, `LOADPCT`, `NOMPOWER`, `LINEV`, `OUTPUTV`,
`BATTV`, `ITEMP`, `LINEFREQ`. From Omnismith: only what 001, 002 and 011 read.

Write (additive only):
- **Schema:** template `ups`, the 22 attributes of FR-001 (`ups_host` as a reference to the
  host template, 011 FR-003), and the five `ups_selftest` options.
- **Entity:** one UPS entity per key (011 FR-011), created holding its key, with `ups_host`.
- **Dimensions:** the twelve dimension and list attributes of FR-001, plus `ups_host` (set
  by the core), on the UPS entity.
- **Metrics:** the nine metric attributes, on the UPS entity.

Owned attributes: all 22 slugs above and the template `ups`, owned by module `ups`.

### apccontrol event coverage

How apcupsd's event scripts map onto what an automation can watch:

| apcupsd event (`/etc/apcupsd/<event>`) | Seen in Omnismith as |
|---|---|
| `powerout`, `onbattery` | `ups_on_battery` → true; `ups_last_transfer_at`/`_reason` change |
| `offbattery`, `mainsback` | `ups_on_battery` → false |
| `changeme` | `ups_replace_battery` → true |
| `commfailure` / `commok` | `ups_comm_lost` → true / false |
| `startselftest` / `endselftest` | `ups_selftest` → `in_progress`, then the result |
| `battdetach`/`battattach`, `loadlimit`, `runlimit`, `timeout`, `doshutdown`, `emergency`, `failing`, `remotedown`, `killpower`, `annoyme` | Not covered (Out of scope). Charge, runtime and `ups_battery_low` show the approach to a shutdown. |

## Edge cases & failure modes

- **Short power dips.** A transfer shorter than the collection interval may never show
  `ups_on_battery` = true; `ups_last_transfer_at` still changes (US-1/4). Automations that
  must see every transfer watch that attribute. Self-test transfers change it too; the
  reason tells them apart.
- **apcupsd's `ONBATTERYDELAY`** (6s in the owner's config) delays apcupsd's event
  scripts. Whether it also delays the status flag is verified at acceptance and recorded.
- **apcupsd restarts.** `XONBATT` becomes `N/A`; the entity keeps its last transfer (FR-013).
- **UPS replaced.** A new serial is a new entity; the old one keeps its history (011
  US-3/2). A static identity keeps one record across the swap (US-6).
- **apcupsd network mode** (another apcupsd as the UPS master). The slave reports the
  master's UPS with the same serial, so both hosts publish one entity and its host
  link follows the last publisher (011 edge cases).
- **Lost communication at startup.** No serial may be known yet. FR-006 then fails each
  collection until a serial is known, unless a static identity is set.
- **Self-test result ages out.** apcupsd reports `NO` a few minutes after a test; the entity
  keeps the result (FR-012). After an omnistat restart the result is whatever the entity
  last had.
- **NIS restricted to loopback, or not enabled** (`NETSERVER off`). The connection is
  refused: FR-017, logged once (FR-018). The README names the apcupsd settings
  (`NETSERVER on`, `NISIP`, `NISPORT`).
- **Several UPSes / several apcupsd instances** on one host: only one address is read
  (Out of scope).
- **Publish latency.** A state change reaches Omnismith at the next publish: at most the
  collection interval plus the publish interval (about 70s at defaults). Lowering
  `publish.interval` shortens it.
- **API failures.** As 003 and 011; nothing here talks to the API.

## Out of scope

- Publishing immediately on a state change (needs a core change to ADR-0005's schedule).
- apcupsd's event log (NIS `events`, `EVENTSFILE`) and status file (`STATFILE`).
- Network UPS Tools (NUT) and other UPS daemons. The module is named `ups`, not
  `apcupsd`, so a later spec can add NUT as a second source for the same attributes.
- Several UPSes or apcupsd instances per host.
- Controlling the UPS or apcupsd (self-test, shutdown, beeper, calibration).
- Links from the hosts a UPS powers to the UPS ("powered by").
- Events and flags not in FR-001: missing battery, shutdown in progress, transfer counts
  and cumulative time on battery, boost/trim, calibration.
- Nominal ratings, firmware, sensitivity and transfer thresholds as dimensions.
- Windows and macOS (FR-020).

## Decisions

Taken by the owner before the spec (2026-10-05):
- **The UPS is its own entity**, linked to the host that reads it (011). Not attributes on
  the host entity.
- **The recommended attribute set** of FR-001 (state flags, last transfer, self-test,
  identity, nine metrics), chosen over a lean set of eight.
- **Next-publish latency is acceptable**; no out-of-cycle publish.
- **NIS, not files.** The status file is written only when `STATTIME` > 0 (the owner's is
  `0`) and holds the same record as NIS. The events file is free text that apcupsd trims
  from the front. A file watcher would also contradict ADR-0005.
- **A built-in NIS reader is preferred** over a third-party client (decided in the plan).
- **Identity**: the serial number, with a static override; never a guessed fallback. It
  is the entity's platform external key, unprefixed (011 FR-007). The serial number stays
  visible as `ups_serial`, so a static identity never stands in for it.
- **Linux only.**

For the owner to confirm with the spec:
- **Module name `ups`** and slug prefix `ups_`; units as slug suffixes (`_pct`, `_min`,
  `_w`, `_v`, `_c`, `_hz`), like `net_rx_mbps`.
- **`ups_name` added** as the record's label (it was not in the proposed list).
- **Self-test options** `passed`, `failed_capacity`, `failed`, `warning`, `in_progress`;
  `NO`/`??` keep the last result.
- **Lost communication publishes only `ups_comm_lost`** (and identity), not stale readings.
- **Disabled by default; collection every 10s; NIS address default `127.0.0.1:3551`.**
- **FR-018 changes failure logging for every module** (first occurrence, then counted).
- **ADR-0015** (accepted 2026-10-05): a provider may make one short, read-only request to
  a local information service named in its config. Today 003 FR-004 and ADR-0005 forbid any
  network access in providers; the ADR amends both, and still forbids the Omnismith API
  and connections that outlive a call.

## Open questions

None. The decisions above await the owner's confirmation with the spec. Two facts are
verified by the plan's spike against apcupsd 3.14's source, not assumed: the full set of
`SELFTEST` codes (FR-002), and which fields and flags apcupsd reports while communication
is lost (FR-014).

## Implementation notes (2026-10-05)

Implemented per `plan.md` (tasks T001–T008). ADR-0015 accepted by the owner, 2026-10-05.
Waiting for the owner's acceptance on the host with the UPS; the status stays
`approved` until then (T009).

- **Sandbox acceptance (NFR-006), 2026-10-05, local API:** `TestSandbox_UPS` passes
  against a fake apcupsd:
  - the schema gains `ups` and `ups_host`, which targets the host template;
  - one UPS record is created holding the serial as its key, found by key at once,
    with `ups_host` showing the host's hostname;
  - its dimensions read back, as does its battery-charge series (95);
  - a second run reuses the same record, and only one record has the serial.

  The six metrics of the first run read back exactly: charge 95, runtime 30.5, load 20,
  estimated 96 W, battery 13.1 V, input 0 V.
- **e2e-systemd, 2026-10-05:** 275/275 on five distros. Inside the 007 sandbox, the
  service read a fake NIS on loopback (`UPS found`), resolved the UPS record by key, and
  logged no collection failure. No sandbox option changed (NFR-003).
- **Real binary, dry-run** against the local API and a fake NIS: the UPS block is shown
  as "to be created", with its host link and twelve dimensions and six metrics.
- **Spike (T001), apcupsd 3.14.14 source:** FR-002's options are complete. The `STATUS`
  text is replaced wholesale by `COMMLOST` and `SHUTTING DOWN`, so the flags come from
  `STATFLAG`'s bits. FR-001's source column was corrected accordingly; FR-014 is
  unchanged. `XONBATT` is *absent*, not `N/A`, before the first transfer; both are
  handled.
- **Deviations:**
  - The "values this UPS does not report" notice (FR-015) is logged whenever the set
    changes, not strictly once per process. A set that never changes is logged once.
  - A valid `XONBATT` without `LASTXFER` publishes neither, and `last_transfer_reason`
    is noted as not reported (FR-013's "together").
  - Units: extra words after the unit are ignored (older apcupsd wrote `29.2 C
    Internal`). `BATTDATE` also accepts `MM/DD/YYYY`.
  - FR-018's repeat counts are a separate `collection failures` record logged with the
    publish summary, not fields of it.
  - Settings (FR-005) are validated for every registered module, enabled or not, so a
    typo is caught before the module is switched on.
- **Covered by tests:** the NIS exchange against an in-process fake server (refused,
  hung, oversized frame, endless reply, early close, cancellation, connection closed on
  every path); recorded replies for a USB Back-UPS on mains, a Smart-UPS on battery, a
  shutdown, lost communication, no serial, invalid values and every self-test code;
  the CLI end to end against a fake NIS and `omnitest`; the skip on Windows and macOS.
- **Not verified (owner acceptance):** real UPS replies (the fixtures are written from
  apcupsd's source); pulling the mains, unplugging the USB cable and stopping apcupsd on
  the real host; `ONBATTERYDELAY`'s effect on the flag.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md`
