---
feature: 005-memory-module
status: implemented        # draft | review | approved | implemented | superseded
approved: 2026-09-24
implemented: 2026-09-24
created: 2026-09-24
owners: [evgenii]
supersedes: null
adr: [0005, 0007, 0008, 0009]
depends_on: [001-module-schema-reconciliation, 002-host-identity, 003-run-loop-publisher, 004-cpu-module]
---

# Feature: `memory` module — how close a host is to swapping

## Summary

Operators want to know when a host is running out of memory, meaning close enough that
the OS will start swapping or killing processes. They also want to compare that pressure
across machines of very different sizes. omnistat reports CPU (004) but nothing about
memory. This feature adds the `memory` module, which reports three values on the host
entity: the share of physical memory in use and the memory still available without
swapping, both as metrics, and the machine's physical memory size as a dimension.

The key decision is **which** "free" to report. What operators need is the OS's own
estimate of memory that can be handed to programs without swapping. The OS's literal
"free" figure is not that number, and on some platforms it is not maintained at all.

## Users & context

- **Operator.** Runs omnistat as a service on Linux hosts and VMs. Wants an alert in
  Omnismith before a host starts swapping: a used-memory percentage that rises as
  headroom shrinks, plus the absolute headroom for a threshold like "less than 512 MiB
  left". Also runs `omnistat run` once from a script and expects real numbers.
- **Fleet operator.** Runs hosts from 1 GiB to hundreds of GiB. Needs a size-independent
  measure (a percentage) to compare them, and the machine's memory size alongside so
  "90% used" can be read against capacity.
- Runs on Linux (primary) and macOS, typically unprivileged. As with `cpu`, the Windows
  readings are specified so the module is complete when omnistat itself runs on Windows.
  That is not part of this feature (004 "Out of scope").

## User stories

### US-1 — Alert before a host swaps (P1)
As an operator, I want the host entity to carry the percentage of physical memory in
use, sampled regularly, so that I can chart it and alert on it in Omnismith.

**Acceptance scenarios**
1. **Given** a reconciled project, an existing host entity and the default module set
   on Linux, **When** the daemon runs for one publish interval, **Then** the host entity
   has received memory-used observations, each stamped with its collection instant and
   each between 0 and 100.
2. **Given** a host whose memory is mostly held by reclaimable file cache, **When** the
   module is collected, **Then** that cache does **not** count as used: the percentage
   reflects memory the OS could not hand out without swapping.
3. **Given** a one-shot `omnistat run`, **When** it completes, **Then** it has published
   a real memory-used value from that single collection, with no warm-up pause.

### US-2 — See the absolute headroom (P1)
As an operator, I want the memory still available without swapping, as an amount, so
that I can alert on "less than N MiB left" regardless of the machine's size.

**Acceptance scenarios**
1. **Given** a Linux host, **When** the module is collected, **Then** the available
   amount is the OS's own estimate of memory available to start new programs without
   swapping, in whole MiB, and it agrees with what the OS's own tools report as
   "available".
2. **Given** any collection that reports both, **Then** the used percentage and the
   available amount come from the same reading of the OS and are consistent with each
   other and with the total.

### US-3 — Compare hosts of different sizes (P1)
As a fleet operator, I want each host's physical memory size on the entity, so that I
can group, filter and compare hosts by capacity and read percentages against it.

**Acceptance scenarios**
1. **Given** any supported host, **When** the module is collected, **Then** the host's
   total physical memory as the OS reports it, in whole MiB, is published as a dimension.
2. **Given** a VM whose memory is resized while omnistat runs, **When** the next
   collection is published, **Then** the dimension carries the new size.

### US-4 — Silent about what a platform cannot measure honestly (P2)
As a fleet operator, I want omnistat to collect what a platform can report and not
invent the rest, while my project schema stays the same across the fleet.

**Acceptance scenarios**
1. **Given** a macOS host, **When** omnistat starts, **Then** it logs once that the
   used-percentage and available attributes are unsupported on this platform. They are
   never collected, and the total is still published.
2. **Given** that host and an empty project, **When** the schema is applied, **Then** all
   three `memory` attributes are created, exactly as from a Linux host.
3. **Given** that host, **When** the dry-run prints what would be published, **Then** the
   unsupported attributes appear once as skipped with their reason, not as failures.

### US-5 — Turn it off (P2)
As an operator, I want to disable `memory` on a host where I do not want memory data,
so that neither its schema nor its values are produced there.

**Acceptance scenarios**
1. **Given** `memory` disabled in config, **When** I dry-run the schema, **Then** none of
   its attributes appear in the plan, and attributes created earlier are left untouched.
2. **Given** `memory` disabled, **When** the daemon runs, **Then** it is never collected
   and the startup schedule log does not list it.

## Functional requirements

### Module manifest
- **FR-001** The `memory` module MUST declare exactly these attributes on the host
  template, with these default slugs and kinds:

  | Key | Default slug | Kind | Human name | Meaning |
  |-----|--------------|------|------------|---------|
  | `used_pct` | `mem_used_pct` | metric | Memory used | Percent of physical memory that is not available without swapping: (total − available) ÷ total × 100 |
  | `available` | `mem_available_mib` | metric | Memory available | Memory the OS estimates can be given to programs without swapping, in whole MiB |
  | `total` | `mem_total_mib` | number | Memory total | Physical memory the OS reports, in whole MiB |

- **FR-002** The module MUST be enabled by default and MUST be disablable in config
  (001 FR-006). It is not required in the sense of 002 FR-002.
- **FR-003** Every attribute MUST be remappable (slug, template and creation-time
  name/description) like any other (001 FR-007, FR-008).
- **FR-004** The module's default collection interval MUST be **30s**, overridable per
  003 FR-003 within the 1s–24h bounds.

### Values
- **FR-005** `available` MUST be the operating system's **own** estimate of memory
  available to programs without swapping: on Linux, the kernel's "available memory"
  figure; on Windows, the OS's available-physical-memory figure. omnistat MUST NOT
  compute its own estimate from free, cache or page counts, and MUST NOT publish the
  OS's literal "free" figure under this slug. It is expressed in MiB (2²⁰ bytes),
  rounded **down** to a whole number.
- **FR-006** `total` MUST be the physical memory size the OS reports as usable, in MiB
  rounded down to a whole number. It MAY be less than the installed capacity (memory
  reserved by firmware or the kernel). omnistat reports what the OS reports.
- **FR-007** `used_pct` MUST be computed from the **same reading** as `available` and
  `total`, and from their exact byte values, not from the rounded MiB values. It is
  (total − available) ÷ total × 100, clamped to `[0, 100]`, and published rounded to
  two decimal places (half away from zero), like `cpu_usage_pct`.
  *(Amended 2026-09-24: values had been published with full float precision, e.g.
  `51.794210150282716`.)*
- **FR-008** A reading whose total is zero MUST produce no observation at all (FR-012).
  A reading whose available amount exceeds its total is inconsistent. It MUST NOT be
  published as 0% used: `used_pct` and `available` are omitted and reported (FR-013),
  and `total` is still observed.
- **FR-009** `total` MUST be observed on every collection, not only the first. It is
  effectively static, and the core's buffer keeps only the latest dimension value
  (003 FR-007), so this costs nothing extra per publish.
- **FR-010** The module keeps **no** state between collections: every value is a
  current level, not a rate, so the previous-reading rules of ADR-0006 do not apply.
  A collection takes one reading and does not pause.

### Platform support (ADR-0007)
- **FR-011** The module MUST declare `total` collectable on Linux, macOS and Windows,
  and `used_pct` and `available` collectable on **Linux and Windows only**. macOS
  maintains no OS estimate of memory available without swapping. Any figure computed
  from its page counts leaves out compressed and purgeable memory, so publishing it
  under these slugs would be a different quantity (FR-005, ADR-0007). On macOS these
  attributes are not collected, are reported once at startup (004 FR-023), and are
  still declared in the schema (004 FR-020).

### Partial collection
- **FR-012** The module MUST produce every value it can. The collection as a whole MUST
  fail only when **no** observation could be produced (the reading failed, or its total
  was zero), in which case it is an ordinary provider failure (003 FR-010).
- **FR-013** Omitted observations MUST be reported in **one** record per collection,
  naming each attribute key and the reason. An attribute that is unsupported on this
  platform (FR-011) is not an omission and MUST NOT be reported this way.

### Observability
- **FR-014** The startup schedule log (003 FR-027) MUST list `memory` with its effective
  collection interval and, once, any attributes that are uncollectable on this platform
  with the reason.
- **FR-015** Collected values MUST be logged at debug level only (003 FR-026).

## Non-functional requirements
- **NFR-001** (performance) One collection takes a single OS reading and completes in
  well under 10ms on a typical VM.
- **NFR-002** (resources) The module holds nothing between collections. At the default
  30s/60s cadence it adds two metric observations per publish and no request of its own
  (003 NFR-003).
- **NFR-003** (safety) Collection MUST require no elevated privileges, MUST execute no
  external command and MUST write nothing to the host.
- **NFR-004** (testability) Every value MUST derive from an injectable source of OS
  readings, so that FR-005…FR-013 are testable in `go test` with no real memory
  pressure, and the per-platform decisions of FR-011 are testable without the platform.
  The static binary MUST still build for every target of `make crosscheck` with
  `CGO_ENABLED=0`.
- **NFR-005** (acceptance) The module MUST be proven end to end against the sandbox
  project: schema applied, values ingested, the metrics read back as a time series and
  the dimension read back from the entity. Acceptance runs on Linux. macOS and Windows
  readings are covered by unit tests and by the cross-compilation gate only.
- **NFR-006** (one reading source) The OS readings used by `cpu` and `memory` MUST come
  from one shared reading facility in the core (ADR-0009), so that the rules of ADR-0008
  (stateless reads, OS-maintained values only) are enforced in one place. Moving `cpu`
  onto it MUST change none of `cpu`'s observable behaviour: its 004 tests pass with only
  mechanical edits.

## Data & integration contract

Read: nothing new from Omnismith beyond 001 (schema and resolved ids) and 002 (the host
entity). From the host: the OS's physical memory size and its own estimate of available
memory, through unprivileged OS interfaces.

Write (additive only, all on the host entity resolved by 002):
- **Metrics:** `mem_used_pct` and `mem_available_mib`, on platforms that maintain an
  available-memory estimate, ingested by the publisher of 003 FR-012 with their
  collection timestamps.
- **Dimension:** `mem_total_mib` (number).

Owned attributes: `mem_used_pct`, `mem_available_mib`, `mem_total_mib`, all owned by
module `memory`. No other module may declare them.

## Edge cases & failure modes

- **Host with lots of file cache.** Cache the OS can reclaim counts as available, not
  used (US-1/2). The OS's literal "free" figure would show such a host as nearly full;
  that is why it is not published.
- **Container with a memory limit.** The readings are the host's, not the cgroup's, so
  `mem_used_pct` reflects the host. This is documented, not corrected; cgroup-aware
  memory is out of scope (as for `cpu` in 004).
- **Linux kernel older than 3.14** (no kernel "available" estimate; long end-of-life;
  RHEL/CentOS 7's 3.10 backports it). The reading source substitutes its own estimate
  and does not say so, so omnistat cannot detect it. This is a **known, accepted
  exception** to ADR-0008's "OS-maintained values only", and such kernels are
  unsupported. Recorded here rather than hidden.
- **VM memory resize / balloon / hotplug.** The next collection publishes the new
  `mem_total_mib`. Percentages stay comparable.
- **Available greater than total** (a misbehaving virtualised source). FR-008 applies:
  the pair is omitted and reported, and the total is kept.
- **Reading fails entirely.** Ordinary provider failure (FR-012, 003 FR-010).
- **macOS.** Only `mem_total_mib` is collected (FR-011).
- **Windows.** The OS also publishes its own integer "memory load" percentage. It is not
  used: `mem_used_pct` follows FR-007 on every platform so that one slug means one
  formula across the fleet. It can differ from Task Manager by rounding.
- **Host with more than 16 TiB.** Whole-MiB values exceed the range that single-precision
  readers keep exact, so a reader may see such a value off by 1 MiB. The value omnistat
  sends is exact.
- **API unreachable / 401 / 403 / 404 / 422 / 429.** Unchanged from 003. Nothing in this
  module touches the API.

## Out of scope

- **Swap**: swap size, swap used, and swap-in/swap-out rates. Swap used says swapping
  has already begun, not that it is near. Many hosts have no swap, macOS sizes it
  dynamically, and Windows' page-file semantics differ. The rates are the real thrashing
  signal, but they are counters (ADR-0006) and Linux-specific. A follow-up.
- The OS's literal "free" figure, and breakdowns such as cached, buffers, shared,
  wired, compressed or huge pages.
- An absolute used amount. It is total − available, derivable from what is published.
  It can be added later without changing anything here.
- A macOS available/used figure. Revisit only with an OS-maintained source verified on
  a Mac.
- cgroup- or container-aware memory, per-process memory, NUMA.
- omnistat running on Windows (identity and shutdown are unspecified there; 004).
- Alerting, thresholds, dashboards or automations built on these metrics.

## Decisions taken during review (2026-09-24)

- Lean set: used % and available amount as metrics, total as a dimension. The used
  amount and swap are deferred.
- "Available", not "free": the OS's own estimate of memory usable without swapping,
  which is what "near swap" means.
- Unit: whole **MiB**, which is readable in the UI and in automation thresholds, and
  exact through single-precision read paths up to 16 TiB. Slugs end `_mib`.
- Slugs: `mem_` prefix (short, matches the vocabulary of the OS's own tools) and
  `mem_used_pct` rather than `mem_usage_pct` (says what it measures, and stays
  consistent if a `mem_used_mib` is ever added).
- macOS publishes the total only. Its available figure would be a computed
  approximation that overstates headroom.
- Default interval 30s: memory is a level, not a rate, and sustained pressure is what a
  "near swap" alert needs. That is two points per default publish.
- Windows: the readings are specified and declared, as for `cpu`. omnistat running on
  Windows remains its own feature.
- `memory` is the second module reading the host, and host reading is consolidated into
  one shared core facility now (NFR-006, ADR-0009), rather than at the third.

## Open questions

None.

## Implementation notes (2026-09-24)

Implemented per `plan.md`; `tasks.md` T001–T013 done. Deviations, surprises and gaps:

- **Kernel exception accepted.** The owner accepted the pre-3.14 Linux case during review:
  the reading source emulates the kernel's "available" estimate there, omnistat cannot
  detect it, and such kernels are unsupported. It is untested.
- **Deviation in the `cpu` move (NFR-006).** The plan made both of `cpu`'s sums module
  functions. In the build, the plain sum of the counters (`Total`) stayed a method on the
  shared reading type, because the counters being disjoint is a property of the reading.
  The idle definition (idle + I/O wait, 004 FR-005) stayed in `cpu` as an exported
  function. One `cpu` test changed mechanically (`x.IdleTime()` → `cpu.IdleTime(x)`); no
  other `cpu` test changed, and all pass.
- **Sandbox acceptance (NFR-005)**, against the local API, project "Omnistat Test", `.env`:
  - `schema apply` created all three attributes, with types metric, metric and number.
  - `run --daemon` with a 1s collection interval and a 2s publish interval published
    repeatedly.
  - `mem_used_pct` read back as **7 points with 7 distinct timestamps** (about 43.6–43.8%).
  - `mem_available_mib` read back as 7 whole-MiB points (17892–17955).
  - `mem_total_mib` read back from the entity as `"31820"`, equal to `/proc/meminfo`
    MemTotal ÷ 1024 and to `free -m`.
  - `make sandbox` runs this as `TestSandbox_Memory`.
- **The spike's sandbox half ran late.** The local API was down during T001, so reading
  the number dimension back was first verified by `TestSandbox_Memory` itself, which also
  showed the entity read-back shapes. Both are recorded in the API notes.
- **API finding: how `fields` is sent.** In the OpenAPI spec, `GET /entities/{id}`'s
  `fields` is an **array**, declared `style: form, explode: false`. That declaration
  makes the generated SDKs send it comma-separated (`fields=a,b`). The server accepts
  that form, and also `fields[]=a&fields[]=b`. A bare repeated `fields=a&fields=b` keeps
  only the last value, because PHP overwrites repeated keys without `[]`. omnistat uses
  the SDK, so it is unaffected. *(Corrected 2026-09-24: an earlier version of this note
  said the API "must be comma-separated".)*
- **The 004 "unreproduced flake" is explained, and fixed after this feature shipped.**
  `TestSandbox_Resolve` (002) failed in the first full `make sandbox` run of this
  feature and in 2 of 6 isolated runs. The platform processes writes asynchronously
  (confirmed by its owner), and a probe measured a new entity as unsearchable for about
  **100–280 ms**. The same day, identity resolution (002 FR-012) and schema
  reconciliation (001 FR-024/FR-025) were amended to wait for their own writes. Every
  sandbox read-back now waits too. `TestSandbox_Resolve` then passed 10 of 10 runs.
- **Rounding (amended 2026-09-24).** `mem_used_pct`, like `cpu_usage_pct`, is now
  published with two decimal places (FR-007). The first acceptance run published
  `51.794210150282716`. `TestSandbox_Memory` now also checks the rounding in the
  series it reads back.
- **Dry-run on the acceptance host** showed `mem_total_mib = 31820`, the same as
  `free -m`. `mem_available_mib` was 17988 against 17889 from a `free -m` taken moments
  later; the value moves continuously. With `memory` disabled, `schema plan` reported no
  changes and `run --dry-run` printed no `mem_*` value (US-5).
- **Not verified on macOS or Windows.** Those readings (macOS total only; Windows total
  and available) are covered only by faked-`Reader` unit tests and the six-target
  `make crosscheck` gate, exactly as NFR-005 says. What gopsutil does on those platforms
  was established by reading its source, not by running it. No omnistat run exists on
  either platform, and omnistat does not run on Windows at all yet.
- **Not verified: the startup "unsupported on this platform" line for macOS** (US-4/1,
  FR-014). The core's per-attribute gating that prints it is generic and unit-tested
  since 004; nothing new was run on macOS.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md`
