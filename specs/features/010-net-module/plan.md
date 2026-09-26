---
feature: 010-net-module
status: done             # draft | approved | done
approved: 2026-09-26
spec: ./spec.md
created: 2026-09-26
depends_on: [004-cpu-module, 008-disk-module]
---

# Plan: `net` module — how much traffic the host moves, and whether its network stack is healthy

## Constitution check
- [x] Uses the SDK for all API access (II). No new API operation. The sandbox acceptance
  reuses `ReadSchema`, `FindEntities` and `EntityChart` from 004/005/008.
- [x] Every template/attribute lives in exactly one module manifest with default slugs;
  no hard-coded schema outside manifests (III). The fifteen attributes are declared only
  in `internal/module/network`'s manifest. The provider returns keys, never slugs. No test
  fixture uses the name `net` or a `net_` slug (checked).
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV).
  Unchanged.
- [x] No secret can reach a commit, a flag, or a log line (IV). No new secret. Interface
  names and values are logged at debug only (FR-024). None is a secret.
- [x] Every mutation is covered by dry-run (IV). Unchanged: `run --dry-run` already prints
  values and platform skips.
- [x] Every FR/NFR has a test strategy using a fake API (V). See the table below. Host
  readings are faked through `network.Reader`, and the clock and pause are injected as in
  `cpu` and `disk`. The API stays faked by `omnitest`.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI). One new package, `internal/module/network` (FR-001). `hostread` gains one
  area (NFR-006). No new module dependency: gopsutil `v4.26.8` and `golang.org/x/sys` are
  already in `go.mod`. `network` imports only `manifest`, `module` and `hostread`.

## Technical context
- Go 1.26, `CGO_ENABLED=0`, SDK `v1.0.15`, gopsutil `v4.26.8`, `golang.org/x/sys v0.41.0`,
  all unchanged.
- **Linux**, gopsutil calls (both stateless file reads):
  - `net.IOCountersWithContext(ctx, true)`: `/proc/net/dev`, per interface. Reads
    `BytesRecv/Sent`, `PacketsRecv/Sent`, `Errin/Errout`, `Dropin/Dropout`. `Fifo*` are
    not used.
  - `net.ProtoCountersWithContext(ctx, []string{"tcp", "udp"})`: `/proc/net/snmp`. Reads
    `Tcp: CurrEstab, OutSegs, RetransSegs, OutRsts` and `Udp: InErrors`.
- **Linux**, direct reads in `hostread` (ADR-0009 follow-up; gopsutil does not expose
  them):
  - `/sys/class/net/<if>/device` and `/sys/class/net/<if>/master`: classification (FR-005);
  - `/proc/net/snmp6` `Udp6InErrors` (FR-010);
  - `/proc/net/netstat` `TcpExt: ListenDrops` (FR-013);
  - `/proc/net/sockstat` `TCP: … tw N` (FR-011);
  - `/proc/sys/net/netfilter/nf_conntrack_{count,max}` (FR-014). gopsutil's
    `FilterCounters` reads the same two files, but its error does not let us tell "not
    loaded" from "unreadable", which FR-014 needs.
- **Windows**, direct `iphlpapi.dll` calls in `hostread` (gopsutil is not used for `net`
  on Windows):
  - `GetIfTable2` + `FreeMibTable`, rows decoded as `windows.MibIfRow2`. gopsutil's
    Windows `IOCounters` counts **unicast packets only** (FR-007 needs all packets) and
    gives nothing to classify interfaces with (FR-005).
  - `GetTcpStatisticsEx2(AF_INET|AF_INET6)` (64-bit segment counters, Windows 10 1709 /
    Server 2016); `GetTcpStatisticsEx` when the export is missing.
    gopsutil's Windows `ProtoCounters` returns "not implemented".
  - `GetUdpStatisticsEx(AF_INET|AF_INET6)`.
  `x/sys/windows` has `MibIfRow2` and `FreeMibTable` but none of the other three
  functions, so they are `windows.NewLazySystemDLL("iphlpapi.dll").NewProc(…)` in
  `hostread/net_windows.go`.
- **macOS**: nothing. `hostread`'s darwin build returns `errors.ErrUnsupported`, and the
  manifest gates every attribute off (FR-018). gopsutil's darwin `net` code is never
  compiled in, because only `net_linux.go` imports it.
- Existing seams reused unchanged: `manifest.Attribute.Platforms` and
  `manifest.Collectable` (ADR-0007), the `collect` gating and startup log, including
  the whole-module skip when nothing is collectable (004 FR-019…FR-023), `module.Omissions`
  (ADR-0009), and config intervals and switches (no new config keys).

### Reading-source facts (from gopsutil v4.26.8, kernel and Microsoft docs; T001 verifies Linux)

| Fact | Consequence |
|------|-------------|
| Linux `/proc/net/dev` lists **every** interface in the namespace: `lo`, bridges, veths, bonds, VLANs, tunnels | Summing all of them counts one container download up to three times. `hostread` classifies, and the module counts physical only (FR-005) |
| Linux: an interface backed by a device has `/sys/class/net/<if>/device`; software interfaces (`lo`, `br*`, `veth*`, `bond*`, `wg*`, `ppp*`, `tun*`, VLANs) do not. On the dev host `wlo1` has it and 19 others do not | Classification rule, as `/sys/block` for disks (008) |
| Azure AN: the VF's `master` link points at the synthetic NIC, which is itself device-backed. A bond member's or bridge port's `master` points at a software interface | Rule: device-backed **and** its master, if any, is not device-backed (FR-005). Bond members and bridge ports stay counted |
| Linux `/proc/net/dev` `drop` = `rx_dropped + rx_missed_errors`; `errs` = `rx_errors` | `rx_drops_ps` includes NIC ring-buffer overruns, which is what an engineer wants |
| Linux `/proc/net/snmp` `Tcp:` is shared by IPv4 and IPv6 (`/proc/net/snmp6` has no `Tcp6` lines). `Udp:` is IPv4 only; IPv6 is `Udp6InErrors` in `/proc/net/snmp6` | TCP read once; UDP errors = `Udp.InErrors + Udp6InErrors` (FR-010). No `snmp6` file (IPv6 disabled at boot) → IPv6 adds 0 |
| Linux `__tcp_transmit_skb` adds to `OutSegs` only when the segment extends past `snd_nxt` or carries no data, so **retransmissions are excluded**. Windows `MIB_TCPSTATS2.dw64OutSegs` "does not include retransmitted segments" | One formula on both: `ΔRetransSegs ÷ (ΔOutSegs + ΔRetransSegs)` (FR-012) |
| Linux `CurrEstab` counts ESTABLISHED + CLOSE_WAIT (RFC 4022). So does Windows `dwCurrEstab` | FR-011 as written |
| Linux `/proc/net/sockstat` `tw` is the TIME_WAIT death-row count, shared by IPv4 and IPv6 | One read (FR-011, FR-010). T001 checks it with an IPv6 connection |
| Linux `/proc/sys/net/netfilter/nf_conntrack_count` is missing when `nf_conntrack` is not loaded, and is per network namespace | `ErrNoConntrack` → once-per-process notice (FR-014) |
| Windows `MibIfRow2.InterfaceAndOperStatusFlags`: bit 0 `HardwareInterface`, bit 1 `FilterInterface`. `Type == IF_TYPE_SOFTWARE_LOOPBACK` (24) is loopback | Physical = hardware ∧ ¬filter ∧ ¬loopback (FR-005). Packets = `Ucast + NUcast` (FR-007). All `MibIfRow2` counters are 64-bit |
| Windows `dwRetransSegs`, `dwOutRsts`, `dwInErrs` (UDP) are 32-bit even in `MIB_TCPSTATS2`, and are per family | Summed across families; a wrap is a regression (FR-015) |
| Windows has no listen-drop or TIME_WAIT counter, and no connection tracking | Gated to Linux (FR-018) |
| The 007 unit restricts sockets to `AF_INET AF_INET6 AF_UNIX`, so **no netlink** | Every Linux read is a file under `/proc` or `/sys`, so the sandbox stays as it is (NFR-003). `ProtectKernelTunables` leaves `/proc/sys` readable; T011 proves it under `PrivateUsers` |

## Approach

### 1. `hostread`: the network area (NFR-006, ADR-0009)

`internal/hostread/net.go` (untagged: types, doc, and the pure parsers and classifiers,
so that their tests run on every CI platform):

```go
// IfaceCounters is one interface's cumulative counters, as the OS keeps them.
type IfaceCounters struct {
    ID       string // stable key: Linux name; Windows LUID in hex
    Name     string // for logs: Linux name; Windows alias
    Physical bool   // spec 010 FR-005, decided here per platform
    RxBytes, TxBytes, RxPackets, TxPackets uint64
    RxErrors, TxErrors, RxDrops, TxDrops    uint64
}

// StackCounters is the TCP/UDP MIB, IPv4 and IPv6 summed (FR-010).
type StackCounters struct {
    TCPCurrEstab   uint64 // gauge
    TCPOutSegs     uint64 // excludes retransmissions on both platforms
    TCPRetransSegs uint64
    TCPOutRsts     uint64
    UDPInErrors    uint64
}

type Conntrack struct{ Count, Max uint64 }

var ErrNoConntrack = errors.New("connection tracking not loaded")

type Net struct{}
func (Net) Interfaces(ctx context.Context) ([]IfaceCounters, error) // sorted by ID
func (Net) Stack(ctx context.Context) (StackCounters, error)
func (Net) ListenDrops(ctx context.Context) (uint64, error) // Linux; ErrUnsupported elsewhere
func (Net) TimeWait(ctx context.Context) (uint64, error)    // Linux; ErrUnsupported elsewhere
func (Net) Conntrack(ctx context.Context) (Conntrack, error) // Linux; ErrNoConntrack; ErrUnsupported elsewhere

// Pure helpers, tested everywhere:
func parseSnmp6UDPInErrors(r io.Reader) (uint64, error)
func parseNetstatListenDrops(r io.Reader) (uint64, error)
func parseSockstatTimeWait(r io.Reader) (uint64, error)
func windowsPhysical(flags uint8, ifType uint32) bool
```

Per-OS files:
- `net_linux.go`: `Interfaces` = gopsutil `IOCounters(pernic)` + `classifyNetIn(sysClassNet,
  name)`: `<if>/device` present, and `<if>/master` absent or resolving to an interface
  without `device`. `Stack` = gopsutil `ProtoCounters(tcp, udp)` + `/proc/net/snmp6`
  (a missing file adds 0). `ListenDrops`, `TimeWait` and `Conntrack` read their files
  through the parsers above. A missing `nf_conntrack_count` gives `ErrNoConntrack`.
  Everything else is an ordinary wrapped error.
- `net_windows.go`: `GetIfTable2` → `unsafe.Slice` over the rows → `windowsPhysical` →
  `IfaceCounters` (`ID` = LUID, `Name` = alias); `FreeMibTable` deferred. `Stack` calls
  `GetTcpStatisticsEx2` per family when `proc.Find()` succeeds, else
  `GetTcpStatisticsEx`, then `GetUdpStatisticsEx` per family. A family returning
  `ERROR_NOT_SUPPORTED` adds 0. The C structs `MIB_TCPSTATS`, `MIB_TCPSTATS2` and
  `MIB_UDPSTATS` are declared locally, with a size test on Windows CI. `ListenDrops`,
  `TimeWait` and `Conntrack` return `errors.ErrUnsupported`.
- `net_other.go` (`//go:build !linux && !windows`): every method returns
  `errors.ErrUnsupported`.

`doc.go` gains "(… Disk, Net)", notes that `Net` reads Linux `/proc` and `/sys` and the
Windows IP Helper API directly, and why (gopsutil's Windows packets are unicast-only, and
it has no classification or listen-drop, TIME_WAIT or IPv6 UDP reading).
`hostread_test.go` adds `Net` to the goroutine test and adds smoke tests: `Interfaces`
logs each interface's physical flag and the call's duration; `Stack` gives
`TCPOutSegs > 0` on a host that has talked TCP; `Conntrack` gives values or
`ErrNoConntrack`. Table tests cover `classifyNetIn` over a fake `/sys/class/net` tree
(`eth0`, `wlo1`, `lo`, `docker0`, `veth1`, `bond0` + members, `eth0.100`, `wg0`, and an
Azure-style `enP1s1` whose master is device-backed `eth0`), the three parsers (real
samples from the dev host, including a kernel with extra columns), and `windowsPhysical`.

### 2. `internal/module/network` (FR-001…FR-024)

The package is named `network`, not `net`, so that it does not shadow the standard library
in its own files or in `cmd/omnistat`. The module name stays `net`, as `machineid` is
`machine-id`.

Files: `network.go` (manifest, `Collect`), `iface.go` (reading, priming, interface
rates), `stack.go` (TCP/UDP rates, share, gauges), `reader.go`. (Implemented without the
planned `errors.go`: `hostread`'s errors already name the area that failed.)

- `Name = "net"`, `DefaultInterval = 30s`, `DefaultPrime = 250ms`, `primeSlack = 50ms`
  (FR-004, FR-015, as `disk`).
- Keys as in FR-001. Manifest: every attribute declares `Platforms`, because none is
  collectable everywhere. `bothOS = {"linux", "windows"}` for the eight interface rates
  and the four common TCP/UDP values, and `linuxOnly = {"linux"}` for `tcp_listen_drops_ps`,
  `tcp_time_wait` and `conntrack_used_pct` (FR-018). On darwin, `collect.Sources`
  therefore skips the whole module with one record (US-5/2), and `Collect` is never
  called.
- `Reader` (module-owned, ADR-0008): the five `hostread.Net` methods.
- `Module` fields: `Reader`, `GOOS`, `Now`, `Sleep`, `Prime`, `Log`, plus `mu`,
  `last *reading`, `noIface sync.Once` and `noConntrack sync.Once` (FR-009, FR-014).

```go
type reading struct {
    at     time.Time
    ifaces map[string]hostread.IfaceCounters // physical only, by ID; nil = read failed
    stack  *hostread.StackCounters           // nil = read failed
    listen *uint64                           // nil = read failed or not Linux
}
```

**Taking a reading** (`read(ctx)`): `Interfaces`, `Stack` and, on Linux, `ListenDrops`,
each independently (FR-019). One `at` stamp. Each area's error is kept for the omission
record. Gauges come from `TimeWait` and `Conntrack`, read once per collection outside the
rate reading, because they need no baseline (FR-016).

**Rates** (`rates(prev, cur, goos)`), modelled on `disk.rates` (ADR-0006, FR-015):
- elapsed `≤ 0` → no rates, not an omission;
- **interfaces**: `cur.ifaces` empty (read succeeded) → FR-009 notice once, no values, no
  omission. Otherwise `common` = IDs in both readings; empty → no values, no omission
  (rename or hot-plug). Per quantity (8), a regression on **any** common interface drops
  that quantity for this collection. The others are published:
  `rx/tx_mbps = round2(ΣΔbytes × 8 ÷ s ÷ 1e6)`, and the pps, errors and drops are
  `round2(ΣΔ ÷ s)` (FR-006…FR-008);
- **stack**: needs `prev.stack` and `cur.stack`. `tcp_resets_ps` and `udp_errors_ps` are
  each dropped on their own regression. `tcp_retrans_pct`: dropped if either counter
  regressed; `Δout + Δretrans == 0` → no value, no omission (FR-012); else
  `pct(Δretrans, Δout + Δretrans)`;
- **listen drops** (Linux): needs both `listen` values; `round2(Δ ÷ s)`; a regression
  drops it.

**Gauges** (`stack.go`): `tcp_established` = `cur.stack.TCPCurrEstab`, published on every
collection including the first (FR-016). On Linux, `tcp_time_wait` = `TimeWait()`, and
`conntrack_used_pct` = `pct(Count, Max)`, where `ErrNoConntrack` or `Max == 0` gives the
once-per-process notice with no omission (FR-014). Gauges are published as `float64` of
the whole number.

**`io`-style locking and priming**, copied from `disk.io`: under `mu`, `read`. The first
call primes with `Sleep(ctx, prime)` when `canPrime`, and otherwise stores the baseline
with no rate values and no omission. `m.last` is assigned exactly once on every path. An
area whose read failed stays `nil` in the stored reading, so the next collection has no
baseline for that area and publishes its rates one collection later. That is honest: it
means "no baseline", not a regression.

**`Collect`** (FR-019…FR-022):
1. Rate reading and rates (above), then the gauges.
2. For each area that failed, an omission for its collectable keys, with the reader's
   error as the reason.
3. `om.Log` once. A debug record `net read` names the counted interfaces
   (`Name`s, comma-separated) and whether conntrack was present (FR-024).
4. `len(obs) == 0 && om.Len() > 0` → `om.Err(Name)`: provider failure (FR-020). `len(obs) ==
   0` with nothing omitted (first collection under a tight deadline in a container with
   no conntrack) returns `nil, nil`, not a failure (FR-021). `collect.collectOne`
   already accepts that: it buffers nothing and logs `collected … observations=0` at
   debug.

### 3. Wiring
- `cmd/omnistat/main.go`: `r.Register(network.New()) // spec 010 FR-002`.

### 4. Acceptance (NFR-005)
- `internal/cli/sandbox_net_linux_test.go` (`sandbox && linux`): `TestSandbox_Net`,
  modelled on `TestSandbox_Disk`. Config `modules.net.interval: 1s`,
  `publish.interval: 2s`, 7s daemon. It asserts:
  - all fifteen attributes exist, all metrics;
  - series with ≥ 2 distinct timestamps, values ≥ 0 at 2dp, for the eight interface rates
    (the dev host has a physical `wlo1`), `tcp_resets_ps`, `udp_errors_ps` and
    `tcp_listen_drops_ps`;
  - `tcp_established` and `tcp_time_wait` series of whole numbers ≥ 0;
  - `tcp_retrans_pct` in [0, 100]. The test's own API traffic guarantees segments are sent;
  - conntrack: if the test's own read of `/proc/sys/net/netfilter/nf_conntrack_max`
    succeeds, a series in [0, 100]; otherwise no series and exactly one notice.
  Chart values come back as float32; compare with `twoDecimals32` (008).
- **Linux service** (NFR-003, NFR-005): `scripts/e2e-systemd.sh` gains checks after the
  disk ones. The journal holds the `net read` debug record, and no
  `observations omitted … module=net` record. The containers are on a bridge network, so
  they see only a veth, and the journal must also hold the "no physical network interface"
  notice **once**. The values that are read (TCP gauges, conntrack if loaded on the host)
  prove that the sandbox allows `/proc/net` and `/proc/sys/net` under `PrivateUsers`.
- **Owner acceptance** (no runbook file; outcome recorded in the spec's implementation
  notes):
  - Linux host (Fedora, as the service): `rx_mbps` against `sar -n DEV 30` or
    `ip -s link` deltas during a large download, converted to Mbit/s; the debug record
    names only physical NICs while containers run; TCP values against
    `nstat -az TcpCurrEstab TcpRetransSegs TcpOutSegs TcpOutRsts TcpExtListenDrops`;
    conntrack against `/proc/sys/net/netfilter/nf_conntrack_{count,max}`.
  - Windows VM (006 service, pre-release build): the startup log names
    `tcp_listen_drops_ps`, `tcp_time_wait` and `conntrack_used_pct` as unsupported
    (US-5/1); the debug record names the VM's NIC and **not** loopback, ISATAP/Teredo,
    `vEthernet` or filter interfaces; `rx_mbps` against
    `Get-Counter '\Network Interface(*)\Bytes Received/sec'` × 8 ÷ 10⁶ during a large
    copy; `tcp_established` against `netstat -s -p tcp` "Current Connections" (IPv4 +
    IPv6 sections); no `observations omitted` event for `net`.

### 5. Docs
`README.md` (module table, a paragraph on "physical interfaces" and bits vs bytes, the
Linux-only attributes, config example), `CHANGELOG.md` *Unreleased*, `internal/README.md`
(`hostread` row gains network, new `module/network` row), and the API notes section "Host
readings: network (feature 010 spike)".

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/module/network` | `net` manifest + provider: interface rates, TCP/UDP rates, share and gauges, conntrack | FR-001…FR-024 |
| `internal/hostread` (existing) | `+ Net` reader: Linux interface classification and `/proc` parsers, Windows IP Helper calls | NFR-006, FR-005, FR-010, FR-011, FR-013, FR-014 |
| `internal/cli` (existing, test only) | `TestSandbox_Net` | NFR-005 |

## Data flow
Scheduler tick (30s) → `network.Collect`:
1. `Reader.Interfaces` → keep physical → diff against `m.last.ifaces` over common IDs →
   eight interface rates. On the first call, prime for 250ms.
2. `Reader.Stack` (+ `ListenDrops` on Linux) → diff → `tcp_retrans_pct`, `tcp_resets_ps`,
   `udp_errors_ps`, `tcp_listen_drops_ps`; `tcp_established` from the current reading.
3. Linux: `Reader.TimeWait` → `tcp_time_wait`; `Reader.Conntrack` →
   `conntrack_used_pct` or the once-per-process notice.

The observations (keys) go to the core, which stamps, validates and buffers them. The
publisher ingests up to 15 metrics per collection with their timestamps. On Windows the
core expects none of the three Linux-only keys (004 FR-019) and logs the skip once at
startup (FR-023). On macOS the module is skipped entirely.

## Configuration
No new settings. `modules.net.enabled`, `modules.net.interval` and
`modules.net.attributes.<key>.*` work generically (001, 003, 004).

## Testing strategy
| Requirement | Test | Where |
|-------------|------|-------|
| FR-001, FR-003, FR-018 | `TestManifest_DefaultSlugs` (keys, slugs, kinds, platforms), then `manifest.Validate` | `module/network/network_test.go` |
| FR-002 | the real registry registers `net` enabled; `Enabled({"net": false})` drops it | `module/network` |
| FR-004 | `TestDefaultInterval` = 30s | `network_test.go` |
| FR-005 | `TestCounted_PhysicalOnly` (software interfaces carry the same bytes and are ignored); **`TestRates_ContainerDownload_CountedOnce`**: `eth0`, `docker0` and `vethX` each carry 1 GB and the rate reflects 1 GB once | `iface_test.go` |
| FR-005 (source) | `TestClassifyNetIn` over a fake `/sys/class/net` (incl. bond members counted, bond not; Azure VF not); `TestWindowsPhysical` (hardware, filter, loopback, tunnel) | `hostread/net_linux_test.go`, `hostread/net_test.go` |
| FR-006 | `TestRates_Mbps` (125 000 000 B over 10s → 100.00 Mbit/s, decimal, ×8) | `iface_test.go` |
| FR-007, FR-008 | `TestRates_PacketsErrorsDrops` per direction; `TestRates_IdleIsZeroNotAbsent` | `iface_test.go` |
| FR-009, FR-021 | `TestIface_NoPhysical_NoticeOnceNoOmission` (two collects, one info record, zero omission records, no interface values, no error) | `iface_test.go` |
| FR-010 | `hostread`: `Stack` sums `Udp` + `Udp6`; missing `snmp6` adds 0 (parser/fixture test) | `hostread/net_linux_test.go` |
| FR-011, FR-016 | `TestGauges_FirstCollect` (established and time_wait published on collect 1, whole numbers) | `stack_test.go` |
| FR-012 | `TestRetransPct` (50 of 10 000 total → 0.5; denominator includes retrans), `TestRetransPct_NoSegments_NoValueNoOmission`, clamp | `stack_test.go` |
| FR-013 | `TestRates_ResetsUDPListenDrops` | `stack_test.go` |
| FR-014 | `TestConntrack_Pct`, `TestConntrack_NotLoaded_NoticeOnce`, `TestConntrack_MaxZero_NoticeOnce`, `TestConntrack_LoadedLater` | `stack_test.go` |
| FR-015 | `TestFirstCollectPrimes` (fake Sleep 250ms), `TestTightDeadlineSkipsPrime` (gauges yes, rates no, no omission), `TestCancelledPauseKeepsConsistentBaseline`, `TestHotplugContributesNothing`, `TestRegression_DropsOnlyThatQuantity` (32-bit retrans wrap keeps resets), `TestZeroElapsed`, `TestFailedAreaHasNoBaselineNextTime`; race test for concurrent collects | `iface_test.go`, `stack_test.go`, `race_test.go` |
| FR-017 | one call per `Reader` method per collect (fake counts calls; two on the priming collect) | `network_test.go` |
| FR-019, FR-020 | `TestCollect_InterfacesFail_StackStillPublished`, `TestCollect_StackFails_InterfacesStillPublished`, `TestCollect_AllFail_ProviderError`, `TestCollect_NothingButSuppressed_NotAFailure` | `network_test.go` |
| FR-022 | `TestCollect_OneOmissionRecord` (two areas fail → one record naming both) | `network_test.go` |
| FR-018, US-5 | `TestCollect_Windows_NoLinuxOnlyReads` (fake records that `ListenDrops`, `TimeWait` and `Conntrack` were never called; no omission for them); `TestSources_PlatformGating`: `collect.Sources` with the real manifest skips three attributes on windows and the whole module on darwin | `network_test.go` |
| FR-023 | existing generic startup-log tests (004); checked by hand in T010 dry-run | `collect`, manual |
| FR-024 | `TestCollect_DebugRecordNamesInterfaces` (captured slog) | `network_test.go` |
| NFR-001 | T001 timing on the dev host (19 software interfaces, several containers); smoke test logs the duration | spike, `hostread` |
| NFR-002 | `TestStateDoesNotGrow` over 100 collects with rotating veth-like names | `state_test.go` |
| NFR-003 | review: no exec, no socket, no write; e2e-systemd checks; Windows owner acceptance | `scripts/e2e-systemd.sh` |
| NFR-004 | fakes and injected clock only, no `time.Sleep`; `make crosscheck`; `GOOS=windows go vet` | — |
| NFR-005 | `TestSandbox_Net`, e2e-systemd checks, owner acceptance | `cli/sandbox_net_linux_test.go`, script |
| NFR-006 | `grep -rn gopsutil internal cmd` lists only `hostread`; goroutine smoke test covers `Net` | `hostread` |

## Risks & unknowns
- **Windows classification is untested here.** Whether a Hyper-V guest's synthetic NIC,
  a VMware vmxnet3 and a VirtualBox NIC report `HardwareInterface`, and whether VPN
  adapters (Wintun, TAP) do not, is known only from documentation. Windows acceptance
  checks the debug record's interface list. If a VM NIC is not "hardware", FR-009's
  notice appears and no interface values are published. The fallback is to add
  `ConnectorPresent` or `PhysicalMediumType` to the rule, as a spec amendment.
- **Windows code runs only in CI and on the owner's VM.** There is no local Windows. The
  struct layouts of `MIB_TCPSTATS2` and `MIB_UDPSTATS` are pinned by size tests that run
  on Windows CI. `GetIfTable2`'s row stride comes from `MibIfRow2` in `x/sys`.
- **Linux `rx_dropped` noise** (active-backup bond members, unknown EtherTypes on older
  kernels) can make `rx_drops_ps` non-zero on healthy hosts. It is documented in the spec,
  not filtered.
- **Azure AN / SR-IOV** (`master` rule) is covered only by the fake-tree test. No Azure VM
  is available.
- **Containers** (e2e) see no physical interface, so e2e proves the sandbox permits the
  reads and the FR-009 path, not interface correctness. Correctness comes from the
  sandbox run on the dev host and owner acceptance.
- **macOS**: nothing to verify; the whole-module skip is covered by the `collect` test
  and `make crosscheck`.
- **Very old Windows builds** without `GetTcpStatisticsEx2` fall back to 32-bit segment
  counters. They wrap within hours on a busy host, and each wrap costs one collection's
  `tcp_retrans_pct`. Acceptable, and below ADR-0010's supported floor anyway.

## Decisions taken here that deserve an ADR
- None new. Reading `/proc`, `/sys` and the IP Helper API directly where gopsutil is wrong
  (Windows packets are unicast-only) or silent (classification, IPv6 UDP, listen drops,
  TIME_WAIT) is ADR-0009's documented follow-up. Physical-only counting, the Mbit/s unit
  and the macOS gap are spec decisions (spec "Decisions").
