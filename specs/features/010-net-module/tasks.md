---
feature: 010-net-module
status: done             # draft | in-progress | done
plan: ./plan.md
---

# Tasks: `net` module — how much traffic the host moves, and whether its network stack is healthy

Rules: one task fits in one agent session; each names the files it touches, the
requirement it serves and how it's verified. `[P]` = parallelisable with siblings.
Tick tasks as they land; keep this file honest.

Every task ends green on `make all` unless it says otherwise. No task starts before the
owner approves `spec.md` (status `approved`).

## Phase 0 — De-risking

- [x] **T001** *(throwaway spike, constitution I)* On the dev host, check the Linux
  readings against the OS:
  - gopsutil `IOCounters(pernic)` against `/proc/net/dev` and `ip -s link`, and the
    `/sys/class/net` classification (`device`, `master`) of every interface. Expect
    `wlo1` physical and the bridges, veths, `lo`, `ppp0` and `wg1` not;
  - `ProtoCounters(tcp, udp)` against `nstat -az`; confirm `/proc/net/snmp6` has no
    `Tcp6` lines, and that `sockstat`'s `tw` rises after closing an IPv6 connection;
  - `ListenDrops` rises under a local accept-queue overflow (listener with backlog 1
    that never accepts, plus a burst of connects);
  - conntrack files readable; goroutine count before and after;
  - duration of one full reading (NFR-001, target well under 50ms, with the host's ~20
    interfaces);
  - cross-compile the spike for the six targets, including a Windows stub calling
    `GetIfTable2`/`GetTcpStatisticsEx2` through `NewLazySystemDLL`.
  Re-check the plan's Windows facts against the Microsoft docs once more (flags bit
  positions, `MIB_UDPSTATS` layout).
  (files: `docs/reference/omnismith-api-notes.md` only: new section "Host readings:
  network") — verify: notes updated; spike deleted; plan facts table amended if anything
  differs.
  **Done 2026-09-26.** Every Linux fact held: of 22 interfaces only `wlo1` has a
  `device` link; its counters equal `ip -s link`; `Tcp:` equals `nstat -az`; `snmp6` has
  no `Tcp6`; `ListenDrops` 0 → 36 after 20 connects to a backlog-1 listener; `tw` +10
  after 10 IPv6 active closes; conntrack 232/262144 readable. One full reading
  0.85–2.0ms, no goroutine. Six targets build. Microsoft's `MIB_IF_ROW2` page confirms
  the flag bits. Its `MIB_UDPSTATS` page could not be fetched (network policy), so that
  layout rests on the size test (T004).

## Phase 1 — The reading source

- [x] **T002** Tests first for `hostread.Net` (NFR-006, FR-005, FR-010, FR-011, FR-013,
  FR-014):
  - `TestClassifyNetIn` over a fake `/sys/class/net` tree (eth0, wlo1, lo, docker0,
    veth1, bond0 with two members, eth0.100, wg0, Azure-style VF with a device-backed
    master);
  - parser tests for `snmp6`, `netstat` and `sockstat` from real samples, including extra
    columns and a missing field;
  - `TestWindowsPhysical`;
  - add `Net` to `TestReads_StartNoGoroutines`;
  - smoke tests `TestNet_Interfaces`, `TestNet_Stack` and `TestNet_Conntrack` (log flags
    and duration; skip where unsupported).
  (files: `internal/hostread/{net_test,net_linux_test,hostread_test}.go`)
  — verify: fails to compile (no type yet).
  **Done 2026-09-26.** Failed to compile for the right reason.

- [x] **T003** Implement `hostread.Net` for Linux and the shared parts: `net.go` (types,
  `ErrNoConntrack`, parsers, `windowsPhysical`), `net_linux.go` (`classifyNetIn`,
  gopsutil calls, direct reads), `net_other.go`, and the `doc.go` notes.
  (files: `internal/hostread/*`) — verify: T002 green; `make crosscheck`;
  `grep -rn gopsutil internal cmd` lists only `internal/hostread`.
  **Done 2026-09-26.** On the dev host: 22 interfaces, `wlo1` alone physical; one reading
  0.14ms; conntrack 176/262144.

- [x] **T004** Implement `hostread.Net` for Windows: `net_windows.go` (`GetIfTable2` +
  `FreeMibTable`, `GetTcpStatisticsEx2` with `Ex` fallback, `GetUdpStatisticsEx`, local
  struct declarations) plus `net_windows_test.go` (struct-size tests; a smoke test that
  finds at least one interface and `TCPCurrEstab` readable).
  (files: `internal/hostread/net_windows*.go`) — verify: `GOOS=windows go vet ./...`,
  `make lint` (lints windows too); the Windows tests run in CI when the owner pushes.
  **Done 2026-09-26.** `GOOS=windows` vet, lint and test compile are clean. The three
  `Get*StatisticsEx*` calls go through one generic `statCall`, so the `unsafe` conversion
  (gosec G103) sits on one annotated line, plus the two for `GetIfTable2`. **Not run on
  Windows**: that happens in CI and at owner acceptance.

## Phase 2 — `net` module, tests first

- [x] **T005** Tests: manifest and interfaces (FR-001…FR-009, FR-018). They cover default
  keys, slugs, kinds and platforms, and `manifest.Validate`; the 30s interval; physical
  only; the **container download counted once** test; Mbit/s decimal ×8; packets,
  errors and drops per direction; idle gives zeros; no physical interface gives one
  notice, no omission and no values.
  (files: `internal/module/network/{network_test,iface_test,fake_test}.go`)
  — verify: fails to compile (no package yet).

- [x] **T006** Tests: TCP/UDP and rate mechanics (FR-010…FR-017). They cover gauges on
  the first collect; the retransmit share (denominator includes retransmissions, no
  segments gives no value and no omission, clamp); resets, UDP errors and listen drops;
  conntrack (percent, not loaded, max 0, loaded later); priming; a tight deadline; a
  cancelled pause; hot-plug; a regression dropping only its quantity (32-bit wrap); zero
  elapsed; a failed area with no baseline next time; one call per reader method; state
  does not grow; and the race test.
  (files: `internal/module/network/{stack_test,state_test,race_test}.go`)
  — verify: fails to compile.

- [x] **T007** Tests: `Collect` as a whole (FR-018…FR-024). On Windows the Linux-only
  readers are never called and nothing is omitted for them; each area can fail alone;
  everything failing is a provider error; nothing but suppressed values is not a
  failure; one omission record names every failed area; the debug record names the
  counted interfaces. Add a `collect.Sources` case: the real manifest on darwin skips
  the whole module (US-5/2).
  (files: `internal/module/network/network_test.go`, `internal/collect/scheduler_test.go`)
  — verify: fails to compile.
  **Done 2026-09-26.** The macOS whole-module skip is tested in the module's own test
  (`TestSources_PlatformGating`, which calls `collect.Sources`) rather than in `collect`:
  `collect` already tests the generic skip, and it should not import a real module.

- [x] **T008** Implement `internal/module/network` (`network.go`, `iface.go`, `stack.go`,
  `reader.go`, `errors.go`) per plan §2.
  — verify: T005–T007 green; `make all`; `make test-race`. Mutation check: count software
  interfaces, drop the ×8, leave retransmissions out of the denominator, and remove the
  hot-plug guard. The named test must fail each time.
  **Done 2026-09-26.** 93.9% coverage; race-clean. The mutation check caught a weak test:
  "retransmissions out of the denominator" survived, because 50 of 9 950 and 50 of
  10 000 both round to 0.50. A 100-of-1 000 case (10%, not 11.11%) now kills it; all four
  mutations fail their named test. No `errors.go`: `hostread`'s errors already name the
  area, so the module adds no sentinel of its own.

## Phase 3 — Wiring and acceptance

- [x] **T009** Register `network.New()` in `cmd/omnistat/main.go` (FR-002). Check
  `omnistat schema plan` (fifteen `net_*` metrics), `schema plan` with `net` disabled
  (none, US-6/1), and `run --dry-run`: values against `ip -s link` and `nstat`, and the
  startup log lists `net interval=30s`.
  (files: `cmd/omnistat/main.go`) — verify: the manual output above, recorded in the
  task note.
  **Done 2026-09-26.** `schema plan` against "Omnistat Test" planned exactly the fifteen
  `net_*` metrics; with `net` disabled, "No changes". `run --dry-run` at debug: startup
  log `module=net interval=30s`; `net read … interfaces=wlo1`; 15 observations
  (`tcp_established` 61, `tcp_time_wait` 56, conntrack 0.09%, `tx_pps` 15.76). The first
  window's `tcp_retrans_pct` was 14.29: 1 of 7 segments in 250ms, the idle-host noise the
  spec documents. Throughput accuracy against the OS tools during a known transfer is
  left to owner acceptance.

- [x] **T010** `TestSandbox_Net` (NFR-005, US-1/1, US-1/4, US-3, US-4) per plan §4.
  (files: `internal/cli/sandbox_net_linux_test.go`) — verify: `make sandbox` against the
  local API (check it is up first; use `-count=1`).
  **Done 2026-09-26.** `TestSandbox_Net` passed: fifteen metric attributes created, and
  all fifteen series read back with 6 distinct timestamps each (the host has `wlo1` and
  conntrack). No omission, no notice. The whole `make sandbox` suite passes.

- [x] **T011** Linux service checks in `scripts/e2e-systemd.sh` (NFR-003, NFR-005, FR-009):
  - the `net read` debug record is present;
  - the "no physical network interface" notice appears exactly once;
  - there is no `net` omission record.
  (files: `scripts/e2e-systemd.sh`) — verify: `make e2e-systemd DISTROS=fedora44`, then
  every distro.
  **Done 2026-09-26.** 260/260 checks: fedora44, debian12, ubuntu2404, rocky9 and rocky8
  (systemd 239), 52 each. Two details beyond the plan. The unit restarts during the
  script (upgrade step), so the "said once" count reads only the running process's
  journal (`_PID=`). And "nothing omitted" alone would pass if nothing were collected, so
  a fourth check requires the core's `collected module=net observations=N` with N > 0.

## Phase 4 — Sync

- [x] **T012** Gates: `make all`, `make test-race`, `make crosscheck`, `make specs-check`,
  and `GOOS=windows go vet -tags sandbox ./...`.
  **Done 2026-09-26**, all green (lint for linux and windows: 0 issues).
- [x] **T013** Docs: `README.md` (module table rows, a "physical interfaces" and
  bits-not-bytes paragraph, Linux-only attributes, config example), `CHANGELOG.md`
  *Unreleased*, `internal/README.md` (`hostread` row gains network, new `module/network`
  row).
  **Done 2026-09-26.** Also the README status line and the API notes section from T001.
- [x] **T014** Owner acceptance (Linux host as the service, and Windows VM as the 006
  service), per plan §4; the outcome goes in the spec's implementation notes. If
  Windows counts no interface or the wrong ones (plan Risks), stop and amend FR-005's
  Windows rule before going further.
  **Done 2026-09-27.** The owner confirmed both parts as a whole, with no per-step table
  and no failure reported, so FR-005's Windows rule needed no amendment.
- [x] **T015** Sync: `spec.md` → `implemented` with implementation notes: deviations,
  surprises, and what is **not** verified (macOS entirely; Azure AN; Windows
  classification beyond the owner's VM). This file and `plan.md` → done.
  **Done 2026-09-27.** It ships in 0.5.0; `CHANGELOG.md` has the `[0.5.0]` section.
