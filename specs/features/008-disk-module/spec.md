---
feature: 008-disk-module
status: implemented      # draft | review | approved | implemented | superseded
approved: 2026-09-26
implemented: 2026-09-26
created: 2026-09-26
owners: [evgenii]
supersedes: null
adr: [0005, 0006, 0007, 0008, 0009, 0010]
depends_on: [001-module-schema-reconciliation, 002-host-identity, 003-run-loop-publisher, 004-cpu-module, 005-memory-module]
---

# Feature: `disk` module — is the system volume filling up, and how hard are the disks working

## Summary

A full system volume is one of the most common ways a host stops working. Logs stop,
databases refuse writes, and package updates fail halfway through. A host whose disks
are saturated is slow in ways CPU and memory graphs do not explain. omnistat reports CPU
(004) and memory (005), but nothing about storage. This feature adds the `disk` module.
On the host entity it reports how full the **system volume** is: the share used, the space
still available and the volume's size, plus how close it is to running out of inodes.
It also reports how much **I/O** the host's physical disks are doing: read and write
throughput, read and write operations per second, and how busy the busiest disk is.

All values describe the host as a whole, so they live on the host entity like every
other module's values. Reporting each filesystem or each disk as its own entity is a
different shape of data, and the core cannot publish it yet. It is out of scope.

## Users & context

- **Operator.** Runs omnistat as a service (006 on Windows, 007 on Linux). Wants an alert
  in Omnismith before the system volume fills, both as a percentage ("above 90%") and as
  an absolute amount ("less than 5 GiB left"). Also wants to see when a host is I/O-bound.
  Runs `omnistat run` once from scripts and expects real numbers, rates included.
- **Fleet operator.** Runs hosts with system volumes from 8 GiB to many TiB. Needs a
  size-independent measure (a percentage) to compare them, and the volume's size next to
  it.
- **Where it runs.** Unprivileged, and on Linux inside the 007 service sandbox, which
  mounts the filesystem read-only for the service and hides home directories. On Windows
  it runs as the 006 service's virtual account. Collection must work in both, with no
  elevated rights.
- **Platforms** (ADR-0010). Space and throughput on Linux, macOS and Windows. Busy % and
  inodes on Linux only, because the other platforms do not maintain those values (FR-016).
  Where the platforms differ in what "system volume" or "physical disk" means, the
  difference is stated in FR-006 and FR-011.

## User stories

### US-1 — Alert before the system volume fills (P1)
As an operator, I want the host entity to carry the percentage of the system volume in
use and the space still available, sampled regularly, so that I can chart both and alert
on either.

**Acceptance scenarios**
1. **Given** a reconciled project, an existing host entity and the default module set on
   Linux, **When** the daemon runs for one publish interval, **Then** the host entity has
   received used-percentage and available-space observations for the system volume, each
   stamped with its collection instant. The percentage is between 0 and 100.
2. **Given** a Linux system volume with space reserved for the superuser, **When** the
   module is collected, **Then** the used percentage agrees with the one the OS's own
   `df` reports for `/` (to rounding), and the available space is what an unprivileged
   program could still write, not counting the reserve.
3. **Given** a one-shot `omnistat run`, **When** it completes, **Then** it has published
   real space values from that single collection.

### US-2 — Compare volumes of different sizes (P1)
As a fleet operator, I want each host's system volume size on the entity, so that I can
read "90% used" against capacity and group hosts by it.

**Acceptance scenarios**
1. **Given** any supported host, **When** the module is collected, **Then** the system
   volume's size is published as a dimension, in GiB with two decimal places.
2. **Given** a VM whose system volume is grown online while omnistat runs, **When** the
   next collection is published, **Then** the dimension carries the new size.

### US-3 — See when the disks are the bottleneck (P1)
As an operator, I want the read and write throughput and operation rates of the host's
physical disks, and how busy the busiest one is, so that I can tell an I/O-bound host
from a CPU-bound one.

**Acceptance scenarios**
1. **Given** a Linux host writing steadily to disk, **When** the module is collected,
   **Then** the write throughput and the write operations per second reflect that
   activity over the time since the previous collection. They agree with what the OS's
   own tools (for example `iostat`) report over the same window.
2. **Given** a Linux host whose root filesystem sits on LVM or on a partition, **When**
   it writes 1 GiB, **Then** that 1 GiB is counted once, not once per layer (partition,
   device-mapper, whole disk).
3. **Given** one disk continuously busy and another idle, **When** the module is
   collected on Linux, **Then** the busy percentage is near 100, not the average of the
   two.
4. **Given** a one-shot `omnistat run`, **When** it completes, **Then** it has published
   real rate values, measured over a short window within that single collection
   (ADR-0006).

### US-4 — Catch "disk full" with space left (P2)
As an operator on Linux, I want the share of the system volume's inodes in use, so that
I can alert before a flood of small files makes the volume unwritable while it still
shows free space.

**Acceptance scenarios**
1. **Given** a Linux system volume on a filesystem with a fixed inode table (ext4, XFS),
   **When** the module is collected, **Then** the inode-used percentage agrees with the
   OS's own `df -i` for `/` (to rounding).
2. **Given** a Linux system volume on a filesystem with no inode limit (btrfs), **When**
   omnistat runs, **Then** no inode value is published, this is logged **once** at info
   level, and it is not reported as a failure on every collection.

### US-5 — Silent about what a platform cannot measure honestly (P2)
As a fleet operator, I want omnistat to collect what a platform can report and not
invent the rest, while my project schema stays the same across the fleet.

**Acceptance scenarios**
1. **Given** a Windows or macOS host, **When** omnistat starts, **Then** it logs once
   that the busy-percentage and inode attributes are unsupported on this platform. They
   are never collected, and the other `disk` attributes are.
2. **Given** that host and an empty project, **When** the schema is applied, **Then** all
   nine `disk` attributes are created, exactly as from a Linux host.
3. **Given** that host, **When** the dry-run prints what would be published, **Then** the
   unsupported attributes appear once as skipped with their reason, not as failures.

### US-6 — Turn it off (P2)
As an operator, I want to disable `disk` on a host where I do not want disk data, so
that neither its schema nor its values are produced there.

**Acceptance scenarios**
1. **Given** `disk` disabled in config, **When** I dry-run the schema, **Then** none of
   its attributes appear in the plan, and attributes created earlier are left untouched.
2. **Given** `disk` disabled, **When** the daemon runs, **Then** it is never collected
   and the startup schedule log does not list it.

## Functional requirements

### Module manifest
- **FR-001** The `disk` module MUST declare exactly these attributes on the host
  template, with these default slugs and kinds:

  | Key | Default slug | Kind | Human name | Meaning |
  |-----|--------------|------|------------|---------|
  | `root_used_pct` | `disk_root_used_pct` | metric | System volume used | Percent of the system volume in use, as `df` computes it (FR-007) |
  | `root_available` | `disk_root_available_gib` | metric | System volume available | Space on the system volume an unprivileged program can still write, in GiB |
  | `root_total` | `disk_root_total_gib` | number | System volume size | Size of the system volume, in GiB |
  | `root_inodes_used_pct` | `disk_root_inodes_used_pct` | metric | System volume inodes used | Percent of the system volume's inodes in use |
  | `read_mibps` | `disk_read_mibps` | metric | Disk read throughput | Bytes read from the physical disks per second, in MiB/s |
  | `write_mibps` | `disk_write_mibps` | metric | Disk write throughput | Bytes written to the physical disks per second, in MiB/s |
  | `read_iops` | `disk_read_iops` | metric | Disk read operations | Read operations completed by the physical disks per second |
  | `write_iops` | `disk_write_iops` | metric | Disk write operations | Write operations completed by the physical disks per second |
  | `busy_pct` | `disk_busy_pct` | metric | Busiest disk utilisation | Percent of wall time the busiest physical disk had I/O in progress |

- **FR-002** The module MUST be enabled by default and MUST be disablable in config
  (001 FR-006). It is not required in the sense of 002 FR-002.
- **FR-003** Every attribute MUST be remappable (slug, template and creation-time
  name/description) like any other (001 FR-007, FR-008).
- **FR-004** The module's default collection interval MUST be **30s**, overridable per
  003 FR-003 within the 1s–24h bounds.

### System volume space
- **FR-005** All `root_*` values of one collection MUST come from **one** reading of the
  system volume, and from its exact byte and inode counts, not from rounded values.
- **FR-006** The **system volume** is the filesystem the OS itself lives on and writes
  to:
  - **Linux:** the filesystem mounted at `/`.
  - **Windows:** the volume holding the Windows directory (normally `C:`, but not
    assumed to be).
  - **macOS:** the startup disk's **data volume** (`/System/Volumes/Data`) when it
    exists, and `/` otherwise. From macOS 10.15, `/` is a sealed, read-only system
    snapshot. Its "used" figure stays near constant while the data volume fills the
    shared container, so reporting it would show a full Mac as nearly empty.
- **FR-007** `root_used_pct` MUST be *used* ÷ (*used* + *available*) × 100, the formula
  `df` uses. *Used* is the space the filesystem reports as allocated, and *available* is
  `root_available` in bytes. Space reserved for the superuser is therefore counted
  neither as used nor as available. The value MUST be clamped to `[0, 100]` and published
  rounded to two decimal places (half away from zero), like `cpu_usage_pct`.
- **FR-008** `root_available` and `root_total` MUST be published in GiB (2³⁰ bytes)
  rounded **down** to two decimal places. `root_available` is the space the OS reports as
  available to an unprivileged program. `root_total` is the size the OS reports for the
  filesystem, which MAY be less than the partition or device size.
- **FR-009** `root_inodes_used_pct` MUST be (total inodes − free inodes) ÷ total inodes
  × 100, clamped and rounded as FR-007. When the filesystem reports a total of zero
  inodes, it has no inode limit (btrfs, for example). omnistat MUST then publish no
  inode value and MUST log this **once per process** at info level. It MUST NOT
  report it as an omission (FR-020) on every collection.
- **FR-010** A space reading whose total is zero, or whose used plus available is zero,
  MUST produce no `root_used_pct`, `root_available` or `root_total` observation, and MUST
  be reported as an omission (FR-020). `root_total` MUST be observed on every successful
  collection, not only the first (as 005 FR-009).

### Disk I/O
- **FR-011** The I/O values MUST count each I/O **once**, at the level of the host's
  **physical disks**:
  - **Linux:** whole block devices backed by a device: SATA/SAS/SCSI, NVMe, virtio,
    Xen and the like. Partitions, device-mapper (LVM, LUKS), software RAID (md), loop,
    RAM and compressed-RAM devices MUST NOT be counted, because their I/O is already
    counted on the devices beneath them or never reaches a disk.
  - **Windows:** the OS maintains these counters per **volume**, not per physical disk.
    omnistat MUST sum the fixed volumes that have a drive letter. Volumes do not overlap,
    so nothing is counted twice. I/O to volumes without a letter (recovery, EFI) is not
    counted.
  - **macOS:** the block storage devices the OS reports statistics for (whole disks).
- **FR-012** `read_mibps` and `write_mibps` MUST be the bytes transferred since the
  previous reading, summed over the counted devices and divided by the elapsed time,
  published in MiB/s (2²⁰ bytes) rounded to two decimal places. `read_iops` and
  `write_iops` MUST be the completed operations since the previous reading, summed and
  divided the same way, published as operations per second rounded to two decimal
  places.
- **FR-013** `busy_pct` MUST be, **per counted device**, the time the device had I/O in
  progress since the previous reading ÷ the elapsed time × 100. The value published is
  the **maximum** over the devices, clamped to `[0, 100]` and rounded as FR-007. An
  average across disks would hide one saturated disk behind idle ones (US-3/3).
- **FR-014** Rate collection MUST follow ADR-0006, exactly as `cpu` does (004
  FR-011…FR-014):
  - the provider retains its previous I/O reading, per device, in memory, for the life
    of the process;
  - on the first collection it primes itself with a second reading after a bounded
    **250ms** pause that honours the call's deadline, and omits the I/O values if the
    deadline would elapse first;
  - a cancelled collection leaves the retained reading consistent: either the old
    reading or the new one, never a mixture;
  - a device present in only one of the two readings (hot-plugged or removed)
    contributes **nothing** to that collection's values. Its lifetime counters MUST
    NOT appear as a one-interval spike;
  - a counter that went backwards on any device (reset, device replaced, a 32-bit
    counter wrapping) MUST NOT produce a negative or absurd value. That collection
    publishes no value that depends on the affected counter. The new reading becomes the
    baseline;
  - two readings with no elapsed time between them produce no I/O values and no
    division error.
- **FR-015** No I/O value may be published when no device could be counted. This
  covers a host with no qualifying device, and a Windows host whose disk performance
  counters are disabled. Such a collection MUST omit the I/O values (FR-020). It MUST
  NOT publish zeros, because "no data" is not "idle".

### Platform support (ADR-0007)
- **FR-016** The module MUST declare the platforms on which each attribute is collectable:

  | Attributes | Linux | Windows | macOS | Why not |
  |------------|:-----:|:-------:|:-----:|---------|
  | `root_used_pct`, `root_available`, `root_total` | ✓ | ✓ | ✓ | — |
  | `read_mibps`, `write_mibps`, `read_iops`, `write_iops` | ✓ | ✓ | ✓ | — |
  | `busy_pct` | ✓ | — | — | Windows keeps an idle-time counter, but per **volume**, and it is not among the readings omnistat takes today. Adding it is a follow-up. macOS maintains no such counter; a figure made from read time + write time double-counts overlapping I/O. |
  | `root_inodes_used_pct` | ✓ | — | — | NTFS has no inode limit. APFS allocates inodes dynamically, so its figure is always near 0% and is not a limit. |

  Attributes not collectable on a platform are not collected there, are reported once at
  startup (004 FR-023) and are still declared in the schema (004 FR-020).

### Partial collection
- **FR-017** The system-volume reading and the I/O reading MUST be independent. Either
  can fail and cost only the values that depend on it.
- **FR-018** The collection as a whole MUST fail only when **no** observation could be
  produced, in which case it is an ordinary provider failure (003 FR-010).
- **FR-019** The module MUST NOT count an inode value that FR-009 suppresses as a
  failure. A Linux host on btrfs with every other value readable is a complete
  collection.
- **FR-020** Omitted observations MUST be reported in **one** record per collection,
  naming each attribute key and the reason (005 FR-013). Not reported this way: an
  attribute unsupported on this platform (FR-016), an inode value suppressed by FR-009,
  and I/O values withheld on the first collection because its deadline was too short
  (FR-014, as 004 FR-012).

### Observability
- **FR-021** The startup schedule log (003 FR-027) MUST list `disk` with its effective
  collection interval and, once, any attributes that are uncollectable on this platform
  with the reason.
- **FR-022** Collected values MUST be logged at debug level only (003 FR-026). The debug
  output SHOULD name the system volume path and the counted devices, so that an
  operator can check what was measured.

## Non-functional requirements
- **NFR-001** (performance) One steady-state collection takes one system-volume reading
  and one I/O reading, and completes in well under 50ms on a typical VM. The first
  collection adds the 250ms priming pause (FR-014).
- **NFR-002** (resources) The provider retains one previous I/O reading: a few counters
  per counted device. At the default 30s/60s cadence the module adds at most 16 metric
  observations per publish and no request of its own (003 NFR-003).
- **NFR-003** (safety) Collection MUST require no elevated privileges, MUST execute no
  external command, MUST write nothing to the host and MUST open no device for writing.
  It MUST work as the 007 Linux service (sandboxed dynamic user, read-only filesystem)
  and as the 006 Windows service account.
- **NFR-004** (testability) Every value MUST derive from an injectable source of OS
  readings and an injected clock. That makes FR-005…FR-020 testable in `go test` with no
  real disk activity and no sleeping, and FR-006, FR-011 and FR-016's per-platform
  decisions testable without the platform. The static binary MUST still build for every
  target of `make crosscheck` with `CGO_ENABLED=0`.
- **NFR-005** (acceptance) The module MUST be proven end to end against the sandbox
  project: schema applied, values ingested, the metrics read back as time series and the
  dimension read back from the entity. Acceptance runs on Linux, both as a foreground
  process and as the 007 service. Because this feature adds Windows behaviour
  (constitution V, ADR-0010), it is also accepted on a `windows/amd64` host running the
  006 service. macOS is covered by unit tests and the cross-compilation gate only.
- **NFR-006** (one reading source) The disk readings MUST come from the shared core
  reading facility (ADR-0009) and obey its rules: stateless reads, OS-maintained values
  only. The retained I/O reading belongs to the provider (ADR-0006).

## Data & integration contract

Read: nothing new from Omnismith beyond 001 (schema and resolved ids) and 002 (the host
entity). From the host: the size, used, available and inode counts of the system
volume, and the cumulative I/O counters of the physical disks, all through unprivileged
OS interfaces.

Write (additive only, all on the host entity resolved by 002):
- **Metrics:** `disk_root_used_pct`, `disk_root_available_gib`,
  `disk_root_inodes_used_pct` (Linux), `disk_read_mibps`, `disk_write_mibps`,
  `disk_read_iops`, `disk_write_iops`, `disk_busy_pct` (Linux), ingested by the publisher
  of 003 FR-012 with their collection timestamps.
- **Dimension:** `disk_root_total_gib` (number).

Owned attributes: the nine slugs above, all owned by module `disk`. No other module may
declare them.

## Edge cases & failure modes

- **Root reserve** (ext4 keeps 5% for the superuser by default). `root_used_pct` can
  show 100 while root can still write, which is what `df` shows. `root_available` is 0
  at that point. Unprivileged services are failing by then, so that is the right
  moment to alert.
- **Container.** `/` is the container's filesystem (for example overlay), whose space
  figures are usually those of the host filesystem backing it. The I/O counters are the
  host's. Documented, not corrected; container-aware disk values are out of scope, as for
  `cpu` and `memory`.
- **The 007 service sandbox** makes every mount read-only for the service and hides home
  directories. Neither changes the system volume's figures or the I/O counters. NFR-005
  proves this by accepting the module as the service.
- **LVM, LUKS, md RAID, partitions.** I/O is counted on the disks beneath them (FR-011).
  A mirrored write therefore counts once per mirror, which is the I/O the disks
  actually did.
- **Disk hot-plug or removal mid-run.** That device contributes nothing to the
  collection spanning the change (FR-014), then is counted normally.
- **Counter reset or 32-bit wrap** (Windows keeps its per-volume operation counts in 32
  bits, so at 10,000 operations per second they wrap after about five days). The affected
  collection publishes no dependent value, and the next one measures from the new
  baseline (FR-014).
- **Windows with disk performance counters disabled** (seen on some Windows Server
  installations). No I/O values; one omission record per collection (FR-015, FR-020). The
  space values are unaffected. Enabling the counters is the operator's fix.
- **Windows system volume not on `C:`.** The volume holding the Windows directory is
  used (FR-006).
- **Mounted disk images, USB and network storage.** Network filesystems carry no
  physical-disk I/O on the host and are not counted. A USB disk is a physical disk and is
  counted. A mounted disk image on macOS may be reported as a disk by the OS and is then
  counted.
- **btrfs or another filesystem with no inode limit.** No inode value; logged once
  (FR-009).
- **Very large volumes.** 2-decimal GiB values stay exact through single-precision
  readers up to about 160 TiB, well past any system volume. The value omnistat sends is
  exact.
- **Reading fails entirely.** Ordinary provider failure (FR-018, 003 FR-010).
- **API unreachable / 401 / 403 / 404 / 422 / 429.** Unchanged from 003. Nothing in this
  module touches the API.

## Out of scope

- **Per-filesystem and per-disk values as their own entities** (a "Filesystem" or "Disk"
  template referencing the host). This needs core support the publisher does not have:
  an identity per child entity, a reference attribute kind, and publishing to more than
  one entity. It would serve network interfaces as well. It is a separate core feature
  first, and disk values per filesystem come after it.
- A "fullest filesystem" aggregate on the host entity. The filtering it needs
  (read-only images, sandbox-hidden mounts) is fragile; per-filesystem entities are the
  honest answer.
- Filesystems other than the system volume: `/var`, `/home`, data volumes, `D:`.
- Disk health (SMART), temperatures, serial numbers, models, partition layout.
- I/O latency and queue depth.
- Swap activity (see 005 "Out of scope").
- cgroup-, container- or process-aware I/O.
- Alerting, thresholds, dashboards or automations built on these metrics.

## Decisions taken during review (2026-09-26)

- **Host-level only.** The system volume plus disk I/O totals, which fit today's core.
  Per-filesystem entities are deferred to a core feature (owner's choice among: system
  volume + I/O; the same plus a fullest-filesystem aggregate; per-filesystem entities).
- **Space in GiB with two decimal places**, chosen over whole MiB (memory's unit) and
  whole GiB. GiB reads naturally for disks, thresholds like "< 5" are easy to write, and
  two decimals keep small VMs meaningful.
- **Extras:** IOPS, busy % and inode usage all included. Busy % and inodes are Linux-only
  (FR-016).
- **Default interval 30s**, as for memory. The rates span the interval, so this is a
  30-second average, which smooths bursts without hiding sustained load.
- **Confirmed by the owner (2026-09-26):**
  - slugs as in FR-001: the `disk_root_` prefix for the system volume (short and
    familiar on Unix, and it reads as "the OS's own volume" on Windows too), and
    `_mibps` / `_iops` for rates;
  - macOS reads the data volume (FR-006), and its inodes are declared unsupported
    (FR-016) rather than published as an always-near-0% value. Both follow from reading
    the OS's behaviour; neither can be run here, since no Mac is available;
  - Windows I/O is volume-level (FR-011), not physical-disk-level. The slug means the
    same thing (bytes the host moved to and from local storage), and unlettered volumes
    are left out;
  - acceptance includes the owner's Windows VM and the Linux service (NFR-005).

## Open questions

None.

## Implementation notes (2026-09-26)

Implemented per `plan.md`; `tasks.md` T001–T016 done.

**Owner acceptance, 2026-09-26.** The owner accepted the feature as a whole. No
per-step results were returned:
- on their Fedora workstation (against `df`, `iostat`, and as the 007 service);
- on their Windows VM (as the 006 service).

Because the owner reported no failure, the Windows virtual-account risk of the plan
(opening `\\.\C:` for the I/O counters) did not occur, and no fallback reader was
needed. Whether the optional ext4/XFS inode step (A3) was run is not recorded. The
feature ships in 0.3.0.

Deviations, surprises and gaps:

- **Found and fixed outside this module: module log records bypassed the process
  logger.** The CLI never made its configured logger slog's default, so a record a
  module writes went to Go's default logger. That logger ignored `log.level` and
  `log.format`, always dropped debug records, and reached neither the journal's
  priorities nor the Windows Event Log. For `disk` this made FR-022's debug record
  unobservable. For `cpu` and `memory` it means their omission records never reached
  the Event Log under the 006 service. Fixed in `cli.App.Run`; 003 FR-026 amended; test
  `TestRun_ModuleLogsUseTheProcessLogger`.
- **Test fixture renamed.** `moduletest.Disk` (module `disk`, slugs `disk_*`) became
  `moduletest.Volume`, so the real module can register beside the fixtures.
- **Spike (T001): every fact in the plan held.** On the dev host (Fedora, btrfs root on
  NVMe), `Usage("/")` equalled `df -B1 /` byte for byte, and `/proc/diskstats` showed
  each partition carrying its disk's I/O (`nvme1n1p3` ≈ 99.8% of `nvme1n1`). The
  `/sys/block` classification put both disks in "disk" and all seven partitions in
  "partition". One `Usage` + `Counters` took 0.2–0.9 ms. See the API notes.
- **Sandbox acceptance (NFR-005)** against the local API, project "Omnistat Test":
  - nine attributes created (8 metric, 1 number);
  - seven series read back with ≥ 7 distinct timestamps each;
  - `disk_root_total_gib` read back as `"929.92"`, equal to the test's own
    `statfs("/")`;
  - btrfs: exactly one inode notice, no inode series and no omission.
  `make sandbox` runs it as `TestSandbox_Disk`.
- **Dry run on the dev host**: 929.92 / 365.71 GiB and 60.50%, against `df`'s 61%.
  During a 3 GiB direct write the first-collect rates were non-zero (55 MiB/s,
  busy 13%). Their agreement with `iostat` was checked in owner acceptance.
- **Service sandbox (NFR-003)**: `make e2e-systemd` gained two checks: the journal holds
  `disk read … path=/ devices=…` with at least one disk, and no `disk` omission record.
  Both passed on all five distros (Fedora 44, Debian 12, Ubuntu 24.04, Rocky 9, Rocky 8;
  240/240 checks in total).
- **Float32 read-back.** The chart returns values as float32, so `365.73` comes back as
  `365.7300109…`. The disk sandbox test checks "two decimals" in float32; the shared
  `twoDecimals` helper's fixed tolerance only holds below about 100.
- **Verified only by the owner's run, confirmed as a whole**: Windows as the 006 service
  account, rate accuracy against `iostat` and `Get-Counter`, and the Linux service on a
  real host. The inode percentage on ext4/XFS is not recorded as verified: the dev host
  is btrfs and the ext4/XFS check was optional. That value rests on unit tests.
- **Not verified at all: macOS.** The data-volume choice, the IOKit disk set and the
  gating are covered only by faked-`Reader` unit tests and the six-target
  `make crosscheck`.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md`
