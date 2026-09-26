---
feature: 008-disk-module
status: done             # draft | approved | done
approved: 2026-09-26
spec: ./spec.md
created: 2026-09-26
depends_on: [004-cpu-module, 005-memory-module]
---

# Plan: `disk` module — is the system volume filling up, and how hard are the disks working

## Constitution check
- [x] Uses the SDK for all API access (II). No new API operation. The sandbox acceptance
  reuses `ReadSchema`, `FindEntities`, `EntityValues` and `EntityChart` from 004/005.
- [x] Every template/attribute lives in exactly one module manifest with default slugs;
  no hard-coded schema outside manifests (III). The nine attributes are declared only in
  `internal/module/disk`'s manifest. The provider returns keys, never slugs. The test
  fixture that squats on the module name `disk` is renamed (§6).
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV).
  Unchanged.
- [x] No secret can reach a commit, a flag, or a log line (IV). No new secret. Values,
  the volume path and device names are logged at debug only (FR-022). None of them is a
  secret.
- [x] Every mutation is covered by dry-run (IV). Unchanged: `run --dry-run` already prints
  values and platform skips.
- [x] Every FR/NFR has a test strategy using a fake API (V). See the table below. Host
  readings are faked through `disk.Reader`, and the clock and pause are injected as in
  `cpu`. The API stays faked by `omnitest`.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI). One new package, `internal/module/disk` (FR-001). `hostread` gains one
  area (NFR-006). No new module dependency: gopsutil `v4.26.8` and `golang.org/x/sys` are
  already in `go.mod`. `disk` imports only `manifest`, `module` and `hostread`.

## Technical context
- Go 1.26, `CGO_ENABLED=0`, SDK `v1.0.15`, gopsutil `v4.26.8`, all unchanged.
- gopsutil calls used (all stateless; see "Reading-source facts"):
  - `disk.UsageWithContext(ctx, path)`, which reads `Total`, `Used`, `Free` (the space
    available to unprivileged users), `InodesTotal` and `InodesFree`. Its `UsedPercent`
    and `InodesUsedPercent` are **never** read: they are gopsutil's arithmetic, and the
    formulas belong to the spec (FR-007, FR-009).
  - `disk.IOCountersWithContext(ctx)`, which reads `ReadBytes`, `WriteBytes`,
    `ReadCount`, `WriteCount` and `IoTime` (Linux only).
- Outside gopsutil, all in `hostread`:
  - Linux: `/sys/block/<name>` and `/sys/block/<name>/device`, to classify devices
    (FR-011).
  - Windows: `windows.GetWindowsDirectory` (`golang.org/x/sys/windows`), for the system
    volume (FR-006).
  - macOS: `os.Stat("/System/Volumes/Data")` (FR-006).
- Existing seams reused unchanged: `manifest.Attribute.Platforms` and
  `manifest.Collectable` (ADR-0007), the `collect` gating and startup log (004
  FR-019…FR-023), `module.Omissions` (ADR-0009), and config intervals and switches (no
  new config keys).

### Reading-source facts (from reading gopsutil v4.26.8; T001 verifies Linux)

| Fact | Consequence |
|------|-------------|
| Linux `Usage`: `Total = f_blocks·bsize`, `Used = (f_blocks − f_bfree)·bsize`, `Free = f_bavail·bsize`, the same as `df` | FR-007 is `Used ÷ (Used + Free)`, which is `df`'s Use% |
| Linux `IOCounters` returns **every** line of `/proc/diskstats`: partitions, `dm-*`, `md*`, `loop*`, `zram*`. It skips only all-zero lines | Summing them counts one write up to three times. `hostread` classifies each device, and the module counts only whole, device-backed disks (FR-011) |
| Linux `IoTime` is diskstats field 13 (ms with I/O in flight), a kernel counter | Source of `busy_pct` (FR-013) |
| macOS `IoTime = ReadTime + WriteTime`, gopsutil's own sum | Emulated, so `busy_pct` is gated to Linux (FR-016) |
| macOS `IOCounters` loads IOKit/CoreFoundation once per process (`sync.Once`) and keeps them open. A failed load is cached for the life of the process | This is a library handle, not a measurement baseline, so it does not break ADR-0008's rule. The sticky failure is recorded in the `hostread` doc |
| Windows `IOCounters` covers **fixed drive letters** only, per volume, via `IOCTL_DISK_PERFORMANCE`. It silently skips volumes whose counters are disabled. It drops `IdleTime`, and `ReadCount`/`WriteCount` are 32-bit | Volume-level sum (FR-011). An empty result means "no data" (FR-015). A counter wrap is a regression (FR-014). `busy_pct` is not collected (FR-016) |
| Windows `IOCounters` returns an error for the **whole call** if opening any fixed volume fails with anything other than "not found" (e.g. access denied) | Risk under the 006 virtual account; checked at Windows acceptance (Risks) |
| Windows `Usage`: `Free = TotalNumberOfFreeBytes` (quota-unaware), `Used = Total − Free` | Without disk quotas, "available to an unprivileged program" equals the volume's free space. Recorded, not corrected |
| Linux `IOCounters` also reads `/run/udev/data/b<maj>:<min>` per device for the serial number and label | Unused, but it costs a few file reads. NFR-001 is checked in T001 |

## Approach

### 1. `hostread`: the disk area (NFR-006, ADR-0009)

`internal/hostread/disk.go`:

```go
// VolumeUsage is one reading of a mounted filesystem, in bytes and inodes.
type VolumeUsage struct {
    Path                    string
    Total, Used, Available  uint64 // Available: space an unprivileged program can use
    InodesTotal, InodesFree uint64 // zero total = no fixed inode table (or none reported)
}

// DeviceKind says what a counter line describes, so that the consumer can
// count each I/O once (spec 008 FR-011). hostread reports the kind;
// deciding which kinds to count is the module's job.
type DeviceKind int
const (
    KindDisk      DeviceKind = iota + 1 // whole, device-backed disk (Linux), whole disk (macOS)
    KindPartition                       // Linux partition
    KindVirtual                         // Linux dm-*, md*, loop*, zram*, ram*: no device beneath
    KindVolume                          // Windows lettered fixed volume
)

// DiskCounters is one device's cumulative I/O counters.
type DiskCounters struct {
    Name                 string
    Kind                 DeviceKind
    ReadBytes, WriteBytes uint64
    ReadOps, WriteOps     uint64
    BusyMillis            uint64 // Linux io_time. Emulated on macOS and absent on Windows: gated
}

type Disk struct{}
func (Disk) Usage(ctx context.Context, path string) (VolumeUsage, error)
func (Disk) Counters(ctx context.Context) ([]DiskCounters, error) // sorted by Name
func (Disk) WindowsDir(ctx context.Context) (string, error)      // errors.ErrUnsupported off Windows
func (Disk) DirExists(path string) bool
```

Per-OS files provide `classify(name string) DeviceKind` and `windowsDir() (string, error)`:
- `disk_linux.go`: `classifyIn(sysBlock, name)`. If `<sysBlock>/<name>` is missing, the
  device is a partition, because partitions are not listed under `/sys/block`. If it is
  present and `<sysBlock>/<name>/device` exists, it is a disk. If it is present with no
  `device`, it is virtual. `classify` calls it with `/sys/block`, and tests call it with
  a temp dir.
- `disk_darwin.go`: every entry is `KindDisk`.
- `disk_windows.go`: every entry is `KindVolume`; `windowsDir` uses
  `windows.GetWindowsDirectory`.
- `disk_notwindows.go` (`//go:build !windows`): `windowsDir` returns
  `errors.ErrUnsupported`.

`doc.go` gains the macOS library-handle note and the `BusyMillis` gating note.
`hostread_test.go` adds `Disk` to the goroutine test and adds smoke tests:
`Usage("/")` gives `Total > 0` and `Used + Available ≤ Total` (available never exceeds
unreserved free space); `Counters` logs the kinds it saw and the call's duration, and
skips rather than fails where the host exposes no disk (some containers). A table test
covers `classifyIn` with a fake `/sys/block` tree: sda (disk), sda1 (partition), dm-0,
md0, loop0, zram0 (virtual), nvme0n1 (disk).

### 2. `internal/module/disk` (FR-001…FR-022)

Files: `disk.go` (manifest, `Collect`), `volume.go` (system-volume path and space
values), `io.go` (rate arithmetic), `reader.go`, `errors.go`.

- `Name = "disk"`, `DefaultInterval = 30s`, `DefaultPrime = 250ms`, `primeSlack = 50ms`
  (same values as `cpu`, FR-004, FR-014).
- Keys: `root_used_pct`, `root_available`, `root_total`, `root_inodes_used_pct`,
  `read_mibps`, `write_mibps`, `read_iops`, `write_iops`, `busy_pct`.
- Manifest (FR-001, FR-016): `busy_pct` and `root_inodes_used_pct` carry
  `Platforms: []string{"linux"}`. All the others declare no platforms.
- `Reader` (module-owned, ADR-0008):
  ```go
  type Reader interface {
      Usage(ctx context.Context, path string) (hostread.VolumeUsage, error)
      Counters(ctx context.Context) ([]hostread.DiskCounters, error)
      WindowsDir(ctx context.Context) (string, error)
      DirExists(path string) bool
  }
  ```
- `Module` fields: `Reader`, `GOOS`, `Now`, `Sleep`, `Prime`, `Log`, plus `mu`, `last`
  (`*ioReading{at time.Time; dev map[string]hostread.DiskCounters}`), and
  `inodeNoticed bool` (FR-009 once-per-process).

**System volume (`volume.go`)**
- `systemVolume(ctx, goos, r) (string, error)` implements FR-006:
  - `linux` → `/`;
  - `darwin` → `/System/Volumes/Data` if `r.DirExists` says so, else `/`;
  - `windows` → `driveRoot(r.WindowsDir())`. `driveRoot` is a pure function:
    `C:\Windows` → `C:\`, anything without a drive letter is an error. It is written by
    hand because `filepath.VolumeName` only parses drive letters when compiled for
    Windows, and the tests run on Linux with `GOOS: "windows"`.
- `spaceObs(u, goos, om)`:
  - FR-010: `Total == 0` or `Used + Available == 0` gives omissions for the three
    `root_*` space keys and no observations.
  - FR-007: `root_used_pct = clampRound2(Used ÷ (Used + Available) × 100)`, computed in
    float64 from the exact bytes.
  - FR-008: `root_available = floorGiB2(Available)`, `root_total = floorGiB2(Total)`.
    `floorGiB2(b) = float64(b>>30) + float64((b&(1<<30−1))*100>>30)/100`. That is
    integer arithmetic, so there is no float rounding up; test pin: `1<<30 − 1` gives
    `0.99`.
  - FR-009 (only when `Collectable(linux)`): `InodesTotal == 0` means one info record per
    process (`inodeNoticed`), with no observation and no omission. Otherwise
    `clampRound2((InodesTotal − InodesFree) ÷ InodesTotal × 100)`. If
    `InodesFree > InodesTotal`, the value is an omission ("inconsistent inode counts").

**I/O (`io.go`)**, modelled on `cpu.usage` (ADR-0006, FR-014):
- `counted(kind)` is true for `KindDisk` and `KindVolume` (FR-011).
- `snapshot(cs)` → `map[name]DiskCounters` of counted devices only.
- `rates(prev, cur *ioReading, goos)` returns observations and omissions:
  - elapsed `≤ 0` → no I/O values (FR-014), not an omission;
  - `common` = device names present in **both** readings (hot-plug, FR-014);
  - `common` empty → omission `errNoDevices` for every collectable I/O key (FR-015);
  - per quantity (read bytes, write bytes, read ops, write ops, busy), a regression on
    **any** common device → that quantity is dropped for this collection. This is not an
    omission; it is FR-014's "no dependent value", like cpu's FR-013;
  - bytes → `round2(Σdelta ÷ elapsed_s ÷ 2²⁰)`, ops → `round2(Σdelta ÷ elapsed_s)`
    (FR-012);
  - busy (Linux only) → `clampRound2(max_dev(ΔBusyMillis) ÷ elapsed_ms × 100)` (FR-013).
- `io(ctx)` under `mu`: read, stamp with `m.now()`, assign `m.last` exactly once on every
  path. The first call primes with `Sleep(ctx, prime)` when `canPrime`; otherwise the
  reading becomes the baseline with no values and no omission (FR-014, FR-020). A read
  error leaves the baseline untouched and is an omission for the collectable I/O keys.
  A snapshot with zero counted devices is also stored, so the next call's `common` set is
  honest.

**`Collect`** (FR-017…FR-020):
1. Resolve the system volume path; on success, `Usage` it. Each error is an omission of
   the space keys (and of inodes on Linux).
2. `io(ctx)`.
3. `om.Log` once; debug record with path, counted device names and values (FR-022).
4. `len(obs) == 0` → `om.Err(Name)`: provider failure (FR-018). A btrfs host whose only
   gap is inodes is never a failure (FR-019), since the space values are present.

Values are `float64`. `collect.validate` already accepts float64 for number and metric.

### 3. Wiring
- `cmd/omnistat/main.go`: `r.Register(disk.New()) // spec 008 FR-002`.

### 4. Acceptance (NFR-005)
- `internal/cli/sandbox_test.go`: `TestSandbox_Disk`, modelled on `TestSandbox_Memory`:
  config `modules.disk.interval: 1s`, `publish.interval: 2s`, 7s daemon. It asserts:
  - all nine attributes exist: eight metrics and one number (`disk_root_total_gib`);
  - `disk_root_total_gib` read back equals the test's own `syscall.Statfs("/")`
    `Blocks·Bsize` through the same floor-2dp rule;
  - series with ≥ 2 distinct timestamps for `disk_root_used_pct` ([0,100], 2dp),
    `disk_root_available_gib` ((0, total]), the four rates (≥ 0, 2dp) and
    `disk_busy_pct` ([0,100]);
  - inodes: if the test's own statfs reports `Files == 0` (this dev host is btrfs), there
    is **no** `disk_root_inodes_used_pct` series, and the run's log has exactly one
    "no inode limit" record (US-4/2). Otherwise there is a series in [0,100].
- **Linux service** (NFR-003, NFR-005): `scripts/e2e-systemd.sh` gains a check. It
  installs with `log.level: debug`, waits for two collections, and requires the journal
  to hold the `disk` debug record with a path of `/` and at least one counted device,
  and **no** `observations omitted … module=disk` record. This proves the 007 sandbox
  (`ProtectSystem=strict`, `ProtectHome`, `PrivateDevices`, `DynamicUser`) does not
  break the readings. In containers `/` is overlay and the counters are the host's; that
  is fine for this purpose.
- **Owner runbook** `specs/features/008-disk-module/acceptance.md`:
  - Linux host (Fedora, service): compare with `df -B1 /`, `df -i /` and `iostat -dx 30`
    during a `dd` write. Check that an LVM/partitioned root counts a 1 GiB write once
    (US-3/2).
  - An ext4 or XFS Linux host, if available: inode value against `df -i` (US-4/1). The
    dev host is btrfs, so this branch is otherwise covered by unit tests only.
  - Windows VM (006 service, pre-release build): startup log names `busy_pct` and
    `root_inodes_used_pct` as unsupported (US-5/1); `disk_root_*` against
    `Get-Volume -DriveLetter C`; write throughput against
    `Get-Counter '\LogicalDisk(C:)\Disk Write Bytes/sec'` during a large copy; and no
    `observations omitted` event for `disk` (the virtual-account risk below).

### 5. Docs
`README.md` (module table, a paragraph on "system volume" and "physical disks", config
example), `CHANGELOG.md` *Unreleased*, `internal/README.md` rows, and the API notes
section "Host readings: disk (feature 008 spike)".

### 6. Test fixture rename
`moduletest.Disk()` declares module `disk` and `disk_*` slugs. Registering it next to the
real module panics (duplicate name), and it invites slug confusion. Rename it to
`moduletest.Volume()`: module `volume`, template `volume`, slugs `volume_mount`,
`volume_used_pct`, `volume_count`. Update its four uses in
`internal/collect/scheduler_test.go` mechanically. No behaviour changes.

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/module/disk` | `disk` manifest + provider: system volume space, I/O rates | FR-001…FR-022 |
| `internal/hostread` (existing) | `+ Disk` reader, device classification, Windows directory | NFR-006, FR-006, FR-011 |
| `internal/module/moduletest` (existing) | `Disk` fixture renamed `Volume` | FR-002 (the real `disk` must register) |
| `internal/cli` (existing, test only) | `TestSandbox_Disk` | NFR-005 |

## Data flow
Scheduler tick (30s) → `disk.Collect`:
1. `systemVolume` → `Reader.Usage(path)` → `root_used_pct`, `root_available`,
   `root_total`, and on Linux `root_inodes_used_pct` or a once-per-process info record.
2. `Reader.Counters` → keep counted kinds → diff against `m.last` over common devices →
   `read_mibps`, `write_mibps`, `read_iops`, `write_iops`, and on Linux `busy_pct`. The
   first call primes for 250ms.

The observations (keys) go to the core, which stamps, validates and buffers them. The
publisher sends `disk_root_total_gib` as a dimension update and ingests the 8 metrics
with their timestamps. On Windows and macOS the core expects neither `busy_pct` nor
`root_inodes_used_pct` (004 FR-019) and logs the skip once at startup (FR-021).

## Configuration
No new settings. `modules.disk.enabled`, `modules.disk.interval` and
`modules.disk.attributes.<key>.*` work generically (001, 003, 004).

## Testing strategy
| Requirement | Test | Where |
|-------------|------|-------|
| FR-001, FR-003, FR-016 | `TestManifest_DefaultSlugs` (keys, slugs, kinds, platforms), then `manifest.Validate` | `module/disk/disk_test.go` |
| FR-002 | the real registry registers `disk` enabled; `Enabled({"disk": false})` drops it; the fixture rename keeps `collect` tests green | `module/disk`, `collect` |
| FR-004 | `TestDefaultInterval` = 30s | `disk_test.go` |
| FR-005 | one `Usage` call per collect (fake counts calls) | `volume_test.go` |
| FR-006 | `TestSystemVolume_{Linux,DarwinData,DarwinFallback,Windows,WindowsNoDrive}`, `TestDriveRoot` table | `volume_test.go` |
| FR-007 | `TestRootUsedPct_DfFormula` (ext4-style reserve: `Used + Available < Total`), clamp, `…RoundedToTwoDecimals` | `volume_test.go` |
| FR-008 | `TestFloorGiB2` (`1<<30−1` → 0.99, exact GiB, 16 TiB), never rounds up | `volume_test.go` |
| FR-009, FR-019 | `TestInodes_NoLimit_LoggedOnceNotOmitted` (two collects, one info record, zero omission records, no error), `TestInodes_Percent`, `TestInodes_FreeExceedsTotal_Omitted` | `volume_test.go` |
| FR-010 | `TestSpace_ZeroTotal_Omitted`, `TestSpace_TotalOnEveryCollect` | `volume_test.go` |
| FR-011 | `TestCounted_Kinds` (disk and volume counted; partition and virtual not); **`TestRates_LVMOnPartition_CountedOnce`**: sda + sda1 + dm-0 each carry the same 1 GiB and the rate reflects 1 GiB once | `io_test.go` |
| FR-011 (source) | `TestClassifyIn` against a fake `/sys/block` tree | `hostread/disk_linux_test.go` |
| FR-012 | `TestRates_MiBpsAndIops` (exact deltas over 30s, 2dp) | `io_test.go` |
| FR-013 | `TestBusy_MaxNotAverage` (one busy, one idle → ~100), clamp over 100 | `io_test.go` |
| FR-014 | `TestIO_FirstCollectPrimes` (fake Sleep records 250ms), `TestIO_TightDeadlineSkipsPrime` (no I/O values, no omission), `TestIO_CancelledPauseKeepsConsistentBaseline`, `TestIO_HotplugDeviceContributesNothing`, `TestIO_CounterRegression_DropsQuantity` (32-bit wrap), `TestIO_ZeroElapsed`; race test for concurrent collects as in `cpu` | `io_test.go`, `io_race_test.go` |
| FR-015 | `TestIO_NoCountedDevices_OmittedNotZero` (empty list; only partitions) | `io_test.go` |
| FR-016 | `TestCollect_Windows_NoBusyNoInodes`, `TestCollect_Darwin_NoBusyNoInodes` (and no omission for them) | `disk_test.go` |
| FR-017, FR-018 | `TestCollect_UsageFails_IOStillPublished`, `TestCollect_CountersFail_SpaceStillPublished`, `TestCollect_BothFail_ProviderError` | `disk_test.go` |
| FR-020 | `TestCollect_OneOmissionRecord` (space and I/O both omitted → one record naming both) | `disk_test.go` |
| FR-021 | existing generic startup-log tests (004); checked by hand in T013 dry-run | `collect`, manual |
| FR-022 | `TestCollect_DebugRecordNamesPathAndDevices` (captured slog) | `disk_test.go` |
| NFR-001 | T001 timing on the dev host; the `hostread` smoke test logs the duration | spike, `hostread` |
| NFR-002 | the retained state is one map of counted devices (reviewed); no per-collect growth: `TestIO_StateDoesNotGrow` over 100 collects with rotating hot-plug names | `io_test.go` |
| NFR-003 | no exec/write in code (review); e2e-systemd check; Windows runbook | `scripts/e2e-systemd.sh`, `acceptance.md` |
| NFR-004 | fakes and injected clock only, no `time.Sleep` in tests; `make crosscheck` | — |
| NFR-005 | `TestSandbox_Disk`, e2e-systemd check, owner runbook | `cli/sandbox_test.go`, script, `acceptance.md` |
| NFR-006 | `grep -rn gopsutil internal cmd` lists only `hostread`; goroutine smoke test covers `Disk` | `hostread` |

## Risks & unknowns
- **Windows virtual account and `\\.\C:`.** gopsutil opens each fixed volume with zero
  access rights, which normally needs no privilege. If it fails under
  `NT SERVICE\omnistat`, the **whole** counters call errors and Windows publishes no I/O
  values. It is detected at Windows acceptance by the "no omission" check. The fallback,
  per the ADR-0009 follow-up, is our own per-volume `IOCTL_DISK_PERFORMANCE` in
  `hostread/disk_windows.go` that skips an unopenable volume instead of failing all of
  them. It is not built unless needed.
- **Windows Server with disk performance counters disabled** yields an empty result and
  an omission record every 30s (FR-015). That is accepted by the spec. The runbook notes
  `diskperf -Y` as the operator's fix.
- **macOS is unverified**, as for `cpu` and `memory`: the data-volume choice, which IOKit
  objects count as disks (a mounted disk image likely does), and the sticky IOKit load
  failure. Covered by unit tests with a fake `Reader` and by `make crosscheck` only.
- **The inode percentage on ext4/XFS** cannot be accepted on the dev host (btrfs). It
  relies on unit tests plus the owner's optional ext4/XFS step.
- **Containers** (e2e) see an overlay `/` and the host's `/proc/diskstats`. The e2e check
  therefore proves the sandbox does not break reading, not that the values are right.
  Correctness comes from the host sandbox run and the owner's runbook.
- **Linux classification edge cases**: multipath (`dm-*` over two `sd*` paths to one
  LUN) counts each path's I/O, which is still once per I/O since each I/O takes one
  path. Some virtual block drivers (e.g. `rbd`, `nbd`) have a `device` link and count as
  disks, which is correct because they carry I/O that leaves the host. Recorded; no
  special cases.

## Decisions taken here that deserve an ADR
- None new. Device classification is a `hostread` reading and the counting rule stays in
  the module, which is ADR-0009 as written. Reading `/sys/block` directly beside gopsutil
  is ADR-0009's documented follow-up ("implement in `hostread` behind the same per-area
  type"). The spec records the per-filesystem deferral.
