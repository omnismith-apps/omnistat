---
feature: 008-disk-module
status: done             # draft | in-progress | done
plan: ./plan.md
---

# Tasks: `disk` module — is the system volume filling up, and how hard are the disks working

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

Every task ends green on `make all` unless it says otherwise.

## Phase 0 — De-risking

- [x] **T001** *(throwaway spike, constitution I)* On the dev host, check gopsutil's disk
  readings against the OS:
  - `Usage("/")` against `df -B1 /` and `df -i /` (this host is btrfs, so expect
    `InodesTotal == 0`);
  - `IOCounters()` against `/proc/diskstats`, and the `/sys/block` classification of
    every name it returns;
  - goroutine count before and after;
  - duration of one `Usage` + one `IOCounters` (NFR-001, target well under 50ms);
  - cross-compile the spike for the six targets.
  Confirm the Windows and macOS facts in the plan's "Reading-source facts" table against
  the v4.26.8 sources once more.
  (files: `docs/reference/omnismith-api-notes.md` only: new section "Host readings: disk")
  — verify: notes updated; spike deleted; plan facts table amended if anything differs.
  **Done 2026-09-26.** Every plan fact held; Linux values equal `df -B1`/`/proc/diskstats`;
  0.23–0.93ms per `Usage` + `IOCounters`; no goroutines; six targets build.

## Phase 1 — Clear the name, then the reading source

- [x] **T002** Rename the `moduletest.Disk` fixture to `moduletest.Volume` (module,
  template and slugs `volume*`) and update its uses (FR-002, plan §6).
  (files: `internal/module/moduletest/fixtures.go`, `internal/collect/scheduler_test.go`)
  — verify: `make all`; `grep -rn '"disk' internal` finds nothing outside what follows.
  **Done 2026-09-26.** The fixture also backs `moduletest.Registry()`, so
  `module/module_test.go` and `cli/cli_test.go` changed mechanically too (sorted names are
  now `ident, probe, volume`). The inline `disk` manifests in the `manifest`, `schema` and
  `config` tests were left as they are: they are literal data that never reaches a
  registry, so they cannot clash with the real module.

- [x] **T003** Tests first for `hostread.Disk` (NFR-006, FR-011): `TestClassifyIn` over a
  fake `/sys/block` tree (sda, sda1, nvme0n1, dm-0, md0, loop0, zram0); add `Disk` to
  `TestReads_StartNoGoroutines`; smoke tests `TestDisk_Usage` and `TestDisk_Counters`
  (logs kinds and duration, skips where there is no disk).
  (files: `internal/hostread/disk_linux_test.go`, `internal/hostread/hostread_test.go`)
  — verify: fails to compile (no type yet).

- [x] **T004** Implement `hostread.Disk`: `disk.go` (types, `Usage`, `Counters`,
  `DirExists`), `disk_linux.go` (`classifyIn`), `disk_darwin.go`, `disk_windows.go`
  (`classify`, `windowsDir` via `GetWindowsDirectory`), `disk_notwindows.go`, plus
  `doc.go` notes (macOS library handle; `BusyMillis` is Linux-only).
  (files: `internal/hostread/*`) — verify: T003 green; `make crosscheck`;
  `grep -rn gopsutil internal cmd` lists only `internal/hostread`.
  **Done 2026-09-26.** `errNotWindows` lives in `disk_notwindows.go`: the Windows lint
  pass flagged it unused in the shared file.

## Phase 2 — `disk` module, tests first

- [x] **T005** Tests: manifest and system volume (FR-001, FR-003…FR-010, FR-016). They
  cover default slugs, kinds and platforms, and `manifest.Validate`; the 30s interval;
  `systemVolume` per GOOS and `driveRoot`; the `df` formula with a reserve; clamp and
  2dp rounding; `floorGiB2` (never rounds up); zero total; total on every collect; one
  `Usage` per collect; inodes: percent, free > total, and no limit logged once with no
  omission.
  (files: `internal/module/disk/{disk_test,volume_test,fake_test}.go`)
  — verify: fails to compile (no package yet).

- [x] **T006** Tests: I/O (FR-011…FR-015, NFR-002). They cover counted kinds; the
  **LVM-on-partition 1 GiB counted once** test; MiB/s and IOPS over 30s at 2dp; busy is
  the max, not the average, with a clamp; the first collect primes (fake `Sleep` 250ms);
  a tight deadline skips the prime; a cancelled pause keeps a consistent baseline;
  hot-plug contributes nothing; a regression or 32-bit wrap drops only that quantity;
  zero elapsed; no counted devices gives an omission, never zeros; state does not grow;
  and the race test.
  (files: `internal/module/disk/{io_test,io_race_test}.go`)
  — verify: fails to compile.

- [x] **T007** Tests: `Collect` as a whole (FR-016…FR-022). Windows and darwin collect no
  busy and no inodes and report no omission for them; either reading can fail alone;
  both failing is a provider error; one omission record naming both areas; a btrfs-style
  collect is complete and error-free; the debug record names the path and devices.
  (files: `internal/module/disk/disk_test.go`) — verify: fails to compile.

- [x] **T008** Implement `internal/module/disk` (`disk.go`, `volume.go`, `io.go`,
  `reader.go`, `errors.go`) per plan §2.
  — verify: T005–T007 green; `make all`; `make test-race`.
  **Done 2026-09-26.** 91.5% coverage. A mutation check (count partitions, average busy,
  round GiB to nearest, drop the hot-plug guard) made the named test fail each time.
  Tests stay in the external `disk_test` package except `state_test.go` (NFR-002 needs
  to see the retained map).

## Phase 3 — Wiring and acceptance

- [x] **T009** Register `disk.New()` in `cmd/omnistat/main.go` (FR-002). Check
  `omnistat schema plan` (nine `disk_*` attributes), `schema plan` with `disk` disabled
  (no `disk_*`, US-6/1), and `run --dry-run` (values against `df -B1 /`; the startup log
  lists `disk` at 30s, FR-021).
  (files: `cmd/omnistat/main.go`) — verify: the manual output above, recorded in the
  task note.
  **Done 2026-09-26.** `schema plan` against "Omnistat Test" planned exactly the nine
  `disk_*` attributes (8 metric, 1 number); with `disk` disabled it planned none.
  `run --dry-run`: `disk_root_total_gib = 929.92`, `available 365.71`, `used 60.5`
  against `df -B1 /` (998500204544 / 392678731776 → 929.92 / 365.71 GiB; `df` 61%). The
  startup log listed `disk interval=30s`, and the btrfs notice appeared once. During a
  3 GiB `dd oflag=direct` the first-collect rates were non-zero (55.24 MiB/s write,
  busy 13.49%); accuracy against `iostat` is left to owner acceptance.
  **Found and fixed:** module log records bypassed the process logger. The CLI never
  made it slog's default, so the `disk read` debug record (FR-022) was always dropped,
  and `cpu`/`memory` omission records ignored `log.level`/`log.format` and never reached
  the journal's priorities or the Windows Event Log. Fix: `cli.App.Run` sets the process
  logger as the default for the run and restores the previous one. Test
  `TestRun_ModuleLogsUseTheProcessLogger`; 003 FR-026 amended.

- [x] **T010** `TestSandbox_Disk` (NFR-005, US-1/1, US-2/1, US-3/4, US-4/2) per plan §4.
  (files: `internal/cli/sandbox_test.go`) — verify: `make sandbox` against the local API
  (check it is up first; use `-count=1`).
  **Done 2026-09-26.** `TestSandbox_Disk` passed against "Omnistat Test": nine attributes
  created (8 metric + 1 number); seven series with ≥ 7 distinct timestamps each;
  `disk_root_total_gib` read back `"929.92"` = the test's own `statfs("/")`; btrfs, so
  exactly one inode notice and no inode series. The whole `make sandbox` suite passes.
  The test lives in `sandbox_disk_linux_test.go` (`sandbox && linux`: it uses
  `unix.Statfs`). Its first run failed on the test's own precision check: the chart
  returns float32, so `365.73` comes back as `365.7300109…`. The shared `twoDecimals`
  tolerance is too tight past ~100, so the disk test compares in float32
  (`twoDecimals32`).

- [x] **T011** Linux service check in `scripts/e2e-systemd.sh` (NFR-003, NFR-005): a
  debug-level install, then the journal holds the `disk` debug record (path `/`, at least
  one device) and no `disk` omission record.
  (files: `scripts/e2e-systemd.sh`) — verify: `make e2e-systemd DISTROS=fedora44`, then
  every distro.
  **Done 2026-09-26.** 240/240 checks: fedora44 48/48, and debian12, ubuntu2404, rocky9
  and rocky8 192/192. Both disk checks passed on every distro (rocky8 runs systemd 239).
  The checks sit after the upgrade step, which already switches the service to
  `log.level: debug`. They depend on the T009 logger fix: before it, the debug record
  could not reach the journal.

## Phase 4 — Sync

- [x] **T013** Gates: `make all`, `make test-race`, `make crosscheck`, `make specs-check`.
  **Done 2026-09-26**, all green. Also `GOOS=windows go vet -tags sandbox ./...`, because
  the disk sandbox test is Linux-only by build tag.
- [x] **T014** Docs: `README.md` (module table row, "system volume" and "physical disks"
  paragraph, config example), `CHANGELOG.md` *Unreleased*, `internal/README.md` (`hostread`
  row gains disk, new `module/disk` row, `moduletest` note).
- [x] **T015** Owner acceptance (Linux host + Windows VM), with the
  results recorded. If Windows shows the virtual-account failure (plan Risks), stop and
  plan the `hostread` fallback as an amendment before going further.
  **Done 2026-09-26.** The owner confirmed both parts as a whole, with no per-step table
  and no failure reported, so no fallback was needed.
- [x] **T016** Sync: `spec.md` → `implemented` plus implementation notes (deviations,
  surprises, and what is **not** verified: macOS entirely, ext4/XFS inodes if the
  optional step was skipped); this file and `plan.md` → done.
  **Done 2026-09-26.** It ships in 0.3.0; `CHANGELOG.md` has the `[0.3.0]` section.
