---
feature: 010-net-module
status: implemented      # draft | review | approved | implemented | superseded
approved: 2026-09-26
implemented: 2026-09-27
created: 2026-09-26
owners: [evgenii]
supersedes: null
adr: [0005, 0006, 0007, 0008, 0009, 0010]
depends_on: [001-module-schema-reconciliation, 002-host-identity, 003-run-loop-publisher, 004-cpu-module, 008-disk-module]
---

# Feature: `net` module — how much traffic the host moves, and whether its network stack is healthy

## Summary

Network problems on a host show up in two places. The first is the **interfaces**: how
much traffic the NICs carry, and whether they are dropping or corrupting packets. The
second is the **TCP/IP stack**: retransmissions, resets, connections refused at a full
listen queue, a connection-tracking table filling up. An operator looking at
omnistat's CPU, memory and disk graphs cannot see either. This feature adds the `net`
module. On the host entity it reports the traffic, packet, error and drop rates of the
host's **physical interfaces**, in each direction. It also reports the TCP and UDP health
figures that network engineers check during an incident: established connections,
retransmission share, resets sent, listen-queue drops, TIME_WAIT sockets, UDP receive
errors and connection-tracking table use.

All values describe the host as a whole and live on the host entity, as for `disk`.
Values per interface, as their own entities, need the same core support that per-disk
values need (008 "Out of scope"). That is out of scope here.

## Users & context

- **Operator.** Runs omnistat as a service (006 on Windows, 007 on Linux). Wants to see a
  host's traffic next to its CPU, and to alert on the classic signs of trouble: a NIC
  dropping or erroring, TCP retransmitting, a service refusing connections because its
  accept queue is full, a firewall's connection table near its limit. Runs
  `omnistat run` once from scripts and expects real rates.
- **Network engineer.** Reads throughput in **bits per second** with decimal prefixes,
  as link speeds, ISP plans and switch dashboards express it. Compares the host's view
  with the switch port's.
- **Where it runs.** Unprivileged. On Linux inside the 007 service sandbox (read-only
  filesystem, private users, address families limited to IPv4, IPv6 and Unix sockets).
  On Windows as the 006 service's virtual account. Collection must work in both without
  widening either sandbox. It may also run in a container, where the host's physical
  interfaces are not visible.
- **Platforms** (ADR-0010). Linux: everything. Windows: interface values and the TCP/UDP
  values the OS maintains; listen-queue drops, TIME_WAIT and connection tracking are
  Linux-only (FR-018). macOS: **no** value in this feature (FR-018). The only interface
  source available there runs an external command, and no Mac is available to verify a
  native reader. A later spec can add it.

## User stories

### US-1 — See how much traffic the host moves (P1)
As an operator, I want the receive and transmit throughput and packet rates of the host's
physical interfaces, so that I can chart the host's traffic and tell a network-bound host
from a CPU-bound one.

**Acceptance scenarios**
1. **Given** a reconciled project, an existing host entity and the default module set on
   Linux, **When** the daemon runs for one publish interval, **Then** the host entity has
   received receive and transmit throughput (Mbit/s) and packet-rate observations, each
   stamped with its collection instant.
2. **Given** a Linux host receiving a steady 100 Mbit/s download over its NIC, **When**
   the module is collected, **Then** the receive throughput is about 100 and agrees with
   the OS's own tools (for example `sar -n DEV`) over the same window, converted to
   Mbit/s.
3. **Given** a Linux host running containers and a VPN (bridge, veth, tunnel and loopback
   interfaces), **When** a container downloads 1 GB through the host's NIC, **Then** that
   1 GB is counted once, on the NIC, not again on the bridge, the veth or the tunnel.
4. **Given** a one-shot `omnistat run`, **When** it completes, **Then** it has published
   real rate values, measured over a short window within that single collection
   (ADR-0006).

### US-2 — Catch a NIC that drops or corrupts packets (P1)
As a network engineer, I want the error and drop rates of the physical interfaces, per
direction, so that I can tell a bad cable or optic (receive errors), a duplex or carrier
problem (transmit errors), a host that cannot keep up with incoming packets (receive
drops) and a full transmit queue (transmit drops) apart.

**Acceptance scenarios**
1. **Given** a Linux host whose NIC counters show receive errors increasing, **When** the
   module is collected, **Then** the receive-error rate is the increase over the elapsed
   time, per second, and the transmit-error rate is unaffected.
2. **Given** an idle, healthy host, **When** the module is collected, **Then** the four
   error and drop rates are published as 0. A healthy zero is a value, not an absence.

### US-3 — See whether TCP is healthy (P1)
As an operator, I want the number of established TCP connections, the share of TCP
segments retransmitted, the rate of resets sent, and (on Linux) the rate of connections
dropped at listening sockets and the number of TIME_WAIT sockets, so that I can alert on
packet loss, connection refusals, accept-queue overflows and ephemeral-port pressure.

**Acceptance scenarios**
1. **Given** a Linux host with 40 established TCP connections, **When** the module is
   collected, **Then** the established-connections value is 40, as the OS's TCP MIB
   reports it (`nstat`/`netstat -s`).
2. **Given** a host that sent 10 000 TCP segments in a window, 50 of them
   retransmissions, **When** the module is collected, **Then** the retransmission share is
   0.5 (percent).
3. **Given** a Linux service whose accept queue overflows under a connection flood,
   **When** the module is collected, **Then** the listen-drop rate is above 0.
4. **Given** a host that sent no TCP segment during the window, **When** the module is
   collected, **Then** no retransmission share is published for it, and this is not an
   error.

### US-4 — See UDP receive loss and a filling connection-tracking table (P2)
As an operator of DNS, syslog or metrics servers, and of hosts behind their own
firewall, I want the rate of UDP datagrams the host could not deliver to an application
(typically a full receive buffer), and on Linux the share of the connection-tracking
table in use, so that I can alert before the kernel starts dropping packets.

**Acceptance scenarios**
1. **Given** a Linux host with connection tracking loaded, **When** the module is
   collected, **Then** the table-use percentage is entries ÷ limit × 100 and agrees with
   `conntrack -C` against `nf_conntrack_max`.
2. **Given** a Linux host without connection tracking loaded, **When** omnistat runs,
   **Then** no table-use value is published, this is logged **once** at info level, and
   it is not reported as a failure on every collection.
3. **Given** a UDP receiver whose socket buffer overflows, **When** the module is
   collected, **Then** the UDP error rate is above 0.

### US-5 — Silent about what a platform cannot measure honestly (P2)
As a fleet operator, I want omnistat to collect what each platform reports and not
invent the rest, while my project schema stays the same across the fleet.

**Acceptance scenarios**
1. **Given** a Windows host, **When** omnistat starts, **Then** it logs once that the
   listen-drop, TIME_WAIT and connection-tracking attributes are unsupported on this
   platform. They are never collected, and the other twelve `net` attributes are.
2. **Given** a macOS host, **When** omnistat starts, **Then** it logs once that the `net`
   module has nothing collectable on this platform, and never collects it.
3. **Given** any of those hosts and an empty project, **When** the schema is applied,
   **Then** all fifteen `net` attributes are created, exactly as from a Linux host.

### US-6 — Turn it off (P2)
As an operator, I want to disable `net` on a host where I do not want network data, so
that neither its schema nor its values are produced there.

**Acceptance scenarios**
1. **Given** `net` disabled in config, **When** I dry-run the schema, **Then** none of its
   attributes appear in the plan, and attributes created earlier are left untouched.
2. **Given** `net` disabled, **When** the daemon runs, **Then** it is never collected and
   the startup schedule log does not list it.

## Functional requirements

### Module manifest
- **FR-001** The `net` module MUST declare exactly these attributes on the host template,
  all of kind **metric**, with these default slugs:

  | Key | Default slug | Human name | Meaning |
  |-----|--------------|------------|---------|
  | `rx_mbps` | `net_rx_mbps` | Network receive throughput | Bits received by the physical interfaces per second, in Mbit/s (10⁶ bits) |
  | `tx_mbps` | `net_tx_mbps` | Network transmit throughput | Bits sent by the physical interfaces per second, in Mbit/s |
  | `rx_pps` | `net_rx_pps` | Packets received | Packets received by the physical interfaces per second |
  | `tx_pps` | `net_tx_pps` | Packets sent | Packets sent by the physical interfaces per second |
  | `rx_errors_ps` | `net_rx_errors_ps` | Receive errors | Received packets the physical interfaces found faulty, per second |
  | `tx_errors_ps` | `net_tx_errors_ps` | Transmit errors | Packets the physical interfaces failed to send, per second |
  | `rx_drops_ps` | `net_rx_drops_ps` | Receive drops | Received packets discarded before reaching the network stack, per second |
  | `tx_drops_ps` | `net_tx_drops_ps` | Transmit drops | Outgoing packets discarded before transmission, per second |
  | `tcp_established` | `net_tcp_established` | TCP connections established | TCP connections currently established, as the OS's TCP MIB counts them |
  | `tcp_retrans_pct` | `net_tcp_retrans_pct` | TCP retransmitted | Percent of the TCP segments sent that were retransmissions |
  | `tcp_resets_ps` | `net_tcp_resets_ps` | TCP resets sent | TCP segments sent with the reset flag, per second |
  | `tcp_listen_drops_ps` | `net_tcp_listen_drops_ps` | TCP listen drops | Incoming connections dropped at listening sockets, per second |
  | `tcp_time_wait` | `net_tcp_time_wait` | TCP sockets in TIME_WAIT | TCP sockets currently in TIME_WAIT |
  | `udp_errors_ps` | `net_udp_errors_ps` | UDP receive errors | Received UDP datagrams that could not be delivered to an application for a reason other than "no listener", per second |
  | `conntrack_used_pct` | `net_conntrack_used_pct` | Connection tracking used | Percent of the connection-tracking table's limit in use |

- **FR-002** The module MUST be enabled by default and MUST be disablable in config
  (001 FR-006). It is not required in the sense of 002 FR-002.
- **FR-003** Every attribute MUST be remappable (slug, template and creation-time
  name/description) like any other (001 FR-007, FR-008).
- **FR-004** The module's default collection interval MUST be **30s**, overridable per
  003 FR-003 within the 1s–24h bounds.

### Interfaces
- **FR-005** The interface values MUST count each packet **once**, at the host's
  **physical interfaces**:
  - **Linux:** network interfaces backed by a device: Ethernet, Wi-Fi, InfiniBand, USB
    network adapters and the virtual NICs a hypervisor presents to a VM (virtio, ENA,
    vmxnet3, Hyper-V and the like). Loopback, bridges, veth pairs, bonds, VLANs,
    tunnels, VPN and PPP interfaces, macvlan/ipvlan, dummy and other software interfaces
    MUST NOT be counted. Their traffic is either counted on a physical interface too or
    never leaves the host.
  - **Windows:** interfaces the OS marks as hardware interfaces, excluding filter
    (lightweight filter driver) interfaces and loopback, so that a NIC behind a virtual
    switch or a filter driver is counted once.
  A physical interface whose traffic the OS already includes in another counted
  interface MUST NOT be counted again. The known case is a Linux SR-IOV virtual function
  enslaved to a hypervisor's synthetic NIC (Azure accelerated networking).
  An interface MUST be counted whether its link is up or down. A down link simply adds
  nothing.
- **FR-006** `rx_mbps` and `tx_mbps` MUST be the bytes received or sent since the previous
  reading, summed over the counted interfaces, × 8, divided by the elapsed seconds and by
  10⁶, rounded to two decimal places. The bytes are those the OS counts at the interface.
  Link-layer framing the NIC does not report (preamble, inter-frame gap, FCS) is not
  added.
- **FR-007** `rx_pps` and `tx_pps` MUST count **all** packets (unicast, multicast and
  broadcast) since the previous reading, summed and divided the same way, rounded to two
  decimal places.
- **FR-008** `rx_errors_ps`, `tx_errors_ps`, `rx_drops_ps` and `tx_drops_ps` MUST be the
  increase of the OS's per-interface error and discard counters for that direction since
  the previous reading, summed and divided the same way, rounded to two decimal places.
  An increase of zero MUST be published as 0 (US-2/2).
- **FR-009** When no interface qualifies under FR-005, for example in a container that
  sees only a veth, the module MUST publish **no** interface value. It MUST NOT publish
  zeros, because "no interface" is not "idle". This is a steady state, not a failure. It
  MUST be logged **once per process** at info level, and MUST NOT be reported as an
  omission (FR-022) on every collection.

### TCP and UDP
- **FR-010** The TCP and UDP values MUST cover IPv4 and IPv6 together. Where the OS keeps
  separate counters per IP version, they MUST be summed.
- **FR-011** `tcp_established` MUST be the OS's current count of established TCP
  connections, as its TCP MIB reports it (RFC 4022 `tcpCurrEstab`, which also counts
  connections in CLOSE_WAIT). `tcp_time_wait` MUST be the OS's current count of TCP
  sockets in TIME_WAIT. Both are published as whole numbers. They are gauges and need no
  previous reading.
- **FR-012** `tcp_retrans_pct` MUST be the segments retransmitted since the previous
  reading ÷ **all** segments sent in that time (first transmissions plus
  retransmissions) × 100, clamped to `[0, 100]` and rounded to two decimal places (half
  away from zero). When no segment was sent in the window, no value MUST be published,
  and this is not an omission (US-3/4).
- **FR-013** `tcp_resets_ps`, `tcp_listen_drops_ps` and `udp_errors_ps` MUST be the
  increase of the OS's counter since the previous reading ÷ elapsed seconds, rounded to
  two decimal places:
  - `tcp_resets_ps`: TCP segments sent with the RST flag;
  - `tcp_listen_drops_ps`: incoming connection requests dropped at a listening socket,
    whether its accept queue or its SYN queue was full or for another reason;
  - `udp_errors_ps`: UDP datagrams received that could not be delivered for a reason
    other than no listening port (full receive buffer, bad checksum).
- **FR-014** `conntrack_used_pct` MUST be the connection-tracking table's current number
  of entries ÷ its configured maximum × 100, clamped and rounded as FR-012. When
  connection tracking is not loaded, or reports a maximum of zero, the module MUST
  publish no value and MUST log this **once per process** at info level. It MUST NOT be
  reported as an omission on every collection. If tracking is loaded later, values start
  on the next collection.

### Rates (ADR-0006)
- **FR-015** Every rate and share (FR-006…FR-008, FR-012, FR-013) MUST follow ADR-0006,
  as `cpu` and `disk` do (004 FR-011…FR-014, 008 FR-014):
  - the provider retains its previous interface and protocol counter readings in memory,
    for the life of the process;
  - on the first collection it primes itself with a second reading after a bounded
    **250ms** pause that honours the call's deadline, and omits the rate values if the
    deadline would elapse first;
  - a cancelled collection leaves the retained reading consistent: either the old
    reading or the new one, never a mixture;
  - an interface present in only one of the two readings (hot-plugged, removed or
    renamed) contributes **nothing** to that collection's values. Its lifetime counters
    MUST NOT appear as a one-interval spike;
  - a counter that went backwards (reset by a driver reload, a 32-bit counter wrapping)
    MUST NOT produce a negative or absurd value. That collection publishes no value that
    depends on the affected counter, and the new reading becomes the baseline;
  - two readings with no elapsed time between them produce no rate and no division
    error.
- **FR-016** The gauges (`tcp_established`, `tcp_time_wait`, `conntrack_used_pct`) MUST
  come from the current reading only and MUST be published on the first collection too.
- **FR-017** All values of one area MUST come from one reading of that area: the
  interface counters from one reading, the TCP and UDP counters from one reading. Values
  are computed from the exact counters, not from rounded ones.

### Platform support (ADR-0007)
- **FR-018** The module MUST declare the platforms on which each attribute is collectable:

  | Attributes | Linux | Windows | macOS | Why not |
  |------------|:-----:|:-------:|:-----:|---------|
  | the eight interface rates | ✓ | ✓ | — | macOS: the only source available without cgo runs an external command (NFR-003), and a native reader cannot be verified without a Mac. |
  | `tcp_established`, `tcp_retrans_pct`, `tcp_resets_ps`, `udp_errors_ps` | ✓ | ✓ | — | macOS: the TCP statistics are kept in a version-dependent kernel structure, unverifiable here. |
  | `tcp_listen_drops_ps` | ✓ | — | — | Windows maintains no listen-queue drop counter. |
  | `tcp_time_wait` | ✓ | — | — | Windows maintains no TIME_WAIT count; counting it would mean enumerating every connection. |
  | `conntrack_used_pct` | ✓ | — | — | Connection tracking is a Linux netfilter facility. |

  Attributes not collectable on a platform are not collected there, are reported once at
  startup (004 FR-023) and are still declared in the schema (004 FR-020). On macOS no
  attribute is collectable, so the whole module is skipped with one startup record.

### Partial collection
- **FR-019** The interface reading, the TCP/UDP reading, and on Linux the TIME_WAIT and
  connection-tracking readings MUST be independent. Any of them can fail and cost only
  the values that depend on it.
- **FR-020** The collection as a whole MUST fail only when **no** observation could be
  produced and something failed, in which case it is an ordinary provider failure
  (003 FR-010). A collection that produced nothing only because of FR-009, FR-012's "no
  segment sent", FR-014 or the first-collection deadline (FR-015) is not a failure.
- **FR-021** The module MUST NOT count a value that FR-009, FR-012 or FR-014 suppresses as
  a failure. A container with no physical interface and no connection tracking, whose
  TCP values are readable, is a complete collection.
- **FR-022** Omitted observations MUST be reported in **one** record per collection,
  naming each attribute key and the reason (005 FR-013). Not reported this way: an
  attribute unsupported on this platform (FR-018), a value suppressed by FR-009, FR-012
  or FR-014, and rate values withheld on the first collection because its deadline was
  too short (FR-015).

### Observability
- **FR-023** The startup schedule log (003 FR-027) MUST list `net` with its effective
  collection interval and, once, any attributes that are uncollectable on this platform
  with the reason.
- **FR-024** Collected values MUST be logged at debug level only (003 FR-026). The debug
  output SHOULD name the counted interfaces, so that an operator can check what was
  measured.

## Non-functional requirements
- **NFR-001** (performance) One steady-state collection takes one reading per area and
  completes in well under 50ms on a typical VM, including one with hundreds of software
  interfaces (a container host) and tens of thousands of TCP connections. The module
  MUST NOT enumerate sockets or connections. The first collection adds the 250ms priming
  pause (FR-015).
- **NFR-002** (resources) The provider retains one previous reading: a few counters per
  counted interface plus the protocol counters. At the default 30s/60s cadence the
  module adds at most 30 metric observations per publish and no request of its own
  (003 NFR-003).
- **NFR-003** (safety) Collection MUST require no elevated privileges, MUST execute no
  external command, MUST write nothing to the host, MUST open no network socket and MUST
  change no network setting. It MUST work as the 007 Linux service without widening its
  sandbox, and as the 006 Windows service account.
- **NFR-004** (testability) Every value MUST derive from an injectable source of OS
  readings and an injected clock. That makes FR-005…FR-022 testable in `go test` with no
  real traffic and no sleeping, and FR-005 and FR-018's per-platform decisions testable
  without the platform. The static binary MUST still build for every target of
  `make crosscheck` with `CGO_ENABLED=0`.
- **NFR-005** (acceptance) The module MUST be proven end to end against the sandbox
  project: schema applied, values ingested and the metrics read back as time series.
  Acceptance runs on Linux, both as a foreground process and as the 007 service.
  Because this feature adds Windows behaviour (constitution V, ADR-0010), it is also
  accepted on a `windows/amd64` host running the 006 service. Owner acceptance covers:
  throughput against the OS's own tools during a known transfer; the TCP values against
  `nstat`/`netstat -s` (Linux) and `netstat -s -p tcp` (Windows); on Windows, which
  interfaces were counted. macOS is covered by unit tests and the cross-compilation gate
  only.
- **NFR-006** (one reading source) The network readings MUST come from the shared core
  reading facility (ADR-0009) and obey its rules: stateless reads, OS-maintained values
  only. The retained readings belong to the provider (ADR-0006).

## Data & integration contract

Read: nothing new from Omnismith beyond 001 (schema and resolved ids) and 002 (the host
entity). From the host: per-interface cumulative traffic, packet, error and discard
counters with enough information to classify each interface; the OS's cumulative TCP
and UDP MIB counters and current TCP gauges; on Linux the TIME_WAIT count and the
connection-tracking count and limit. All through unprivileged, read-only OS interfaces.

Write (additive only, all on the host entity resolved by 002):
- **Metrics:** the fifteen slugs of FR-001, ingested by the publisher of 003 FR-012 with
  their collection timestamps (listen drops, TIME_WAIT and connection tracking from
  Linux only).
- **Dimensions:** none.

Owned attributes: the fifteen slugs above, all owned by module `net`. No other module may
declare them. Interface addresses belong to the future `ip-address` module, not here.

## Edge cases & failure modes

- **Container (own network namespace).** Only a veth is visible, so no interface value
  (FR-009, one notice). The TCP/UDP values and connection tracking are those of the
  container's namespace, which is the traffic of the container. Documented, not
  corrected. A container with the host's network (`--network host`) sees the host's
  interfaces and counts them.
- **Container host (Docker, Kubernetes node).** Bridges and veths are not counted; the
  traffic they carry is counted once on the NIC (US-1/3). Traffic between two containers
  on the same bridge never reaches a NIC and is not counted.
- **Bond or team.** The member NICs are counted, the bond is not. In active-backup mode
  some drivers count every frame arriving on the inactive member as a receive drop, so
  `rx_drops_ps` can be steadily above 0 on a healthy bond. Documented.
- **Azure accelerated networking (SR-IOV).** The VM sees a synthetic NIC and a virtual
  function enslaved to it; the synthetic NIC's counters include the VF's traffic. Only
  the synthetic NIC is counted (FR-005). Not verifiable here; recorded as a risk.
- **VLANs, PPPoE, VPN tunnels (WireGuard, OpenVPN, IPsec).** Counted on the physical
  interface beneath, including encapsulation overhead. PPP over a serial or USB modem
  that is not a network device is not counted.
- **Linux `rx_dropped` beyond buffer overflows.** Older kernels and some drivers also count
  frames with no protocol handler (unknown EtherType, unconfigured VLAN) as drops.
  Recorded, not corrected; the rate is still "packets the host discarded on receive".
- **Hyper-V host or Windows with a virtual switch.** The physical NIC bound to the switch
  is a hardware interface and is counted; the `vEthernet` adapter is not (FR-005).
  Verified at Windows acceptance.
- **Interface hot-plug, rename or driver reload mid-run.** The interface contributes
  nothing to the collection spanning the change, then is counted normally (FR-015).
- **32-bit counters.** Windows keeps the retransmitted-segment and reset counters in 32
  bits, and 32-bit Linux kernels keep interface counters in 32 bits. A wrap is a
  regression: one collection loses the dependent value (FR-015).
- **Idle host.** Very few segments sent, so one retransmission can be a large share. The
  value is honest; an alert on it should also require traffic. A window with no
  segment at all publishes no share (FR-012).
- **Connection tracking in a network namespace.** The count and limit are the
  namespace's.
- **Windows versions.** The 64-bit TCP segment counters need Windows 10 1709 or Windows
  Server 2016. On older builds the 32-bit counters are used, and they wrap sooner.
- **Reading fails entirely.** Ordinary provider failure (FR-020, 003 FR-010).
- **API unreachable / 401 / 403 / 404 / 422 / 429.** Unchanged from 003. Nothing in this
  module touches the API.

## Out of scope

- **Per-interface values as their own entities** (an "Interface" template referencing the
  host, with link state, speed, MAC and addresses). Needs the same core child-entity
  support as per-disk values (008 "Out of scope").
- **Interface utilisation %** (throughput ÷ link speed). Link speed is missing or
  fictional on most VMs and software interfaces. It belongs with per-interface entities.
- Link state, link speed, MTU, duplex and addresses as dimensions (per-interface data;
  addresses belong to `ip-address`).
- **macOS** values (FR-018). A later spec with a native reader and a Mac to accept on.
- Per-socket, per-process and per-port detail; listening-port inventories.
- Latency, jitter, reachability and DNS probes (active checks, not host readings).
- ICMP, IP-level fragmentation and SCTP counters; TCP states other than ESTABLISHED and
  TIME_WAIT.
- Firewall rule counters, NIC ring-buffer and per-queue statistics.
- Alerting, thresholds, dashboards or automations built on these metrics.

## Decisions

Taken by the owner before the spec (2026-09-26):
- **Attribute set:** the fifteen metrics of FR-001, chosen over a lean set of eight
  (errors and drops summed across directions, no Linux-only saturation signals) and over
  adding busiest-interface utilisation.
- **Throughput in Mbit/s, decimal** (10⁶ bits), the network convention, rather than MiB/s
  as `disk` uses.
- **macOS unsupported** in this feature (FR-018), rather than an unverifiable native
  reader.

For the owner to confirm with the spec:
- **Physical interfaces only** (FR-005), with no fallback to software interfaces when there
  are none (FR-009): a container reports no interface values, logged once. The
  alternative, counting a container's veth when nothing else is visible, would make the
  same slug mean "host NIC traffic" on one entity and "container traffic" on another.
- **"No physical interface" and "no connection tracking" are steady states** logged once
  (FR-009, FR-014), not omissions every 30s. This differs from `disk` FR-015, where no
  disk is abnormal.
- **`tcp_retrans_pct` counts retransmissions in the denominator** (FR-012) and has no
  minimum-traffic floor. An absolute retransmission rate is not published.
- **`tcp_resets_ps` is resets *sent*** (the host refusing or aborting connections). Resets
  received are not counted by either OS in a comparable counter.
- **`tcp_established` follows the MIB definition** (includes CLOSE_WAIT on both OSes).
- **Slugs** as in FR-001: the `net_` prefix; `_mbps`, `_pps` and `_ps` for rates;
  `_pct` for shares; gauges unsuffixed.
- **No dimension.** Nothing host-level about the network is both stable and meaningful
  without per-interface entities.
- **Acceptance** includes the owner's Windows VM and the Linux service (NFR-005).

## Open questions

None. The decisions above await the owner's confirmation with the spec.

## Implementation notes (2026-09-26/27)

Implemented per `plan.md`; `tasks.md` T001–T015 done. The feature ships in 0.5.0.

**Owner acceptance, 2026-09-27.** The owner confirmed the feature as a whole, with no
per-step results and no failure reported, on their Fedora host (as the 007 service) and
their Windows VM (as the 006 service). The Windows classification risk of the plan (a VM
NIC not reported as a hardware interface) therefore did not occur, and FR-005's Windows
rule stands unamended.

- **Spike (T001): every Linux fact in the plan held.** On the dev host only `wlo1` of 22
  interfaces (bridges, 15 veths, `lo`, WireGuard) is device-backed. Its counters equal
  `ip -s link`, the TCP MIB equals `nstat`, `ListenDrops` rose under a forced
  accept-queue overflow, and `tw` counted IPv6 TIME_WAIT. A full reading takes about 1–2
  ms.
- **Sandbox acceptance (NFR-005)** against the local API, project "Omnistat Test":
  fifteen metric attributes created, and all fifteen series read back with 6 distinct
  timestamps each (`make sandbox`, `TestSandbox_Net`).
- **Service sandbox (NFR-003)**: `make e2e-systemd` gained four checks: the module reads,
  it collects values, it omits nothing, and a bridged container logs "no physical network
  interface" once per process. All pass on Fedora 44, Debian 12, Ubuntu 24.04, Rocky 9
  and Rocky 8 (260/260 checks). No sandbox option had to change.
- **Dry run**: on the dev host's first 250ms window, `tcp_retrans_pct` was 14.29 (1 of 7
  segments). That is the idle-host behaviour the spec's edge cases describe, not a
  fault.
- **Deviations**: none in behaviour. The module has no `errors.go` (the reader's errors
  already name the area). The macOS whole-module skip is tested in the module's own tests
  rather than in `collect`.
- **Verified only by the owner's run, confirmed as a whole**: the Windows readings and
  which adapters count as physical there, and throughput against the OS's own tools
  during a transfer. No per-step comparison was recorded.
- **Not verified at all**: Azure accelerated networking (the SR-IOV `master` rule rests
  on a fake-tree test); Windows builds older than 10 1709 (the 32-bit fallback). **macOS**
  is covered by unit tests and the cross-compilation gate only; it collects nothing by
  design.

## Review checklist
- [x] No implementation details (packages, libraries, signatures)
- [x] Every requirement is testable and has an ID
- [x] Every user story has at least one acceptance scenario
- [x] No `[NEEDS CLARIFICATION]` markers remain
- [x] Consistent with `specs/constitution.md`
