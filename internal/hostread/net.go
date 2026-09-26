package hostread

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// IfaceCounters is one network interface's cumulative counters, as the OS
// keeps them. Only differences between two readings mean anything.
type IfaceCounters struct {
	// ID is a stable key for the interface within a boot: its name on Linux,
	// its LUID on Windows (where aliases can be renamed by the user).
	ID string
	// Name is what an operator recognises in a log: the Linux name, the
	// Windows alias.
	Name string
	// Physical says whether the interface is a NIC whose traffic nothing else
	// counted here already includes (spec 010 FR-005). hostread decides it
	// per platform; whether to count it is the consumer's decision.
	Physical bool

	RxBytes, TxBytes     uint64
	RxPackets, TxPackets uint64 // every packet: unicast, multicast and broadcast
	RxErrors, TxErrors   uint64
	RxDrops, TxDrops     uint64
}

// StackCounters is the host's TCP and UDP MIB, IPv4 and IPv6 together
// (spec 010 FR-010).
type StackCounters struct {
	// TCPCurrEstab is a gauge: connections in ESTABLISHED or CLOSE_WAIT
	// (RFC 4022 tcpCurrEstab). The rest are cumulative counters.
	TCPCurrEstab uint64
	// TCPOutSegs excludes retransmitted segments on Linux and on Windows
	// (feature 010 spike); TCPRetransSegs counts those.
	TCPOutSegs     uint64
	TCPRetransSegs uint64
	// TCPOutRsts is segments sent with the RST flag. On Windows it, and the
	// retransmission count, are 32-bit and wrap.
	TCPOutRsts uint64
	// UDPInErrors is datagrams received that could not be delivered for a
	// reason other than no listening port.
	UDPInErrors uint64
}

// Conntrack is the Linux connection-tracking table's size and limit, for the
// network namespace omnistat runs in.
type Conntrack struct {
	Count, Max uint64
}

// ErrNoConntrack means the kernel has no connection tracking loaded (spec 010
// FR-014): a steady state, not a failure to read.
var ErrNoConntrack = errors.New("connection tracking not loaded")

// Net reads network interface and protocol counters (spec 010). Linux reads
// /proc and /sys; Windows calls the IP Helper API; other platforms return
// errors.ErrUnsupported from every method.
type Net struct{}

// Interfaces reads every network interface's counters, each classified,
// sorted by ID.
func (Net) Interfaces(ctx context.Context) ([]IfaceCounters, error) {
	out, err := netInterfaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("network interfaces: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Stack reads the TCP and UDP counters, IPv4 and IPv6 summed.
func (Net) Stack(ctx context.Context) (StackCounters, error) {
	s, err := netStack(ctx)
	if err != nil {
		return StackCounters{}, fmt.Errorf("tcp/udp counters: %w", err)
	}
	return s, nil
}

// ListenDrops reads the cumulative count of incoming connections dropped at
// listening sockets (Linux TcpExt ListenDrops).
func (Net) ListenDrops(ctx context.Context) (uint64, error) {
	n, err := netListenDrops(ctx)
	if err != nil {
		return 0, fmt.Errorf("listen drops: %w", err)
	}
	return n, nil
}

// TimeWait reads the number of TCP sockets in TIME_WAIT, IPv4 and IPv6.
func (Net) TimeWait(ctx context.Context) (uint64, error) {
	n, err := netTimeWait(ctx)
	if err != nil {
		return 0, fmt.Errorf("time-wait sockets: %w", err)
	}
	return n, nil
}

// Conntrack reads the connection-tracking table's size and limit. It
// returns ErrNoConntrack when tracking is not loaded.
func (Net) Conntrack(ctx context.Context) (Conntrack, error) {
	c, err := netConntrack(ctx)
	if err != nil {
		return Conntrack{}, fmt.Errorf("connection tracking: %w", err)
	}
	return c, nil
}

// parseSnmp6UDPInErrors reads Udp6InErrors from /proc/net/snmp6, whose lines
// are "name value".
func parseSnmp6UDPInErrors(r io.Reader) (uint64, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[0] == "Udp6InErrors" {
			return strconv.ParseUint(f[1], 10, 64)
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("no Udp6InErrors")
}

// parseNetstatListenDrops reads TcpExt's ListenDrops from /proc/net/netstat,
// which holds pairs of lines: "TcpExt: name name …" then "TcpExt: n n …".
// The column is found by name, so added kernel columns do not shift it.
func parseNetstatListenDrops(r io.Reader) (uint64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		names := strings.Fields(sc.Text())
		if len(names) == 0 || names[0] != "TcpExt:" {
			continue
		}
		if !sc.Scan() {
			break
		}
		values := strings.Fields(sc.Text())
		for i, n := range names {
			if n != "ListenDrops" {
				continue
			}
			if i >= len(values) {
				return 0, errors.New("TcpExt values line too short")
			}
			return strconv.ParseUint(values[i], 10, 64)
		}
		return 0, errors.New("no TcpExt ListenDrops")
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("no TcpExt values")
}

// parseSockstatTimeWait reads the `tw` field of /proc/net/sockstat's TCP line
// ("TCP: inuse 44 orphan 0 tw 32 alloc 160 mem 891").
func parseSockstatTimeWait(r io.Reader) (uint64, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || f[0] != "TCP:" {
			continue
		}
		for i := 1; i+1 < len(f); i++ {
			if f[i] == "tw" {
				return strconv.ParseUint(f[i+1], 10, 64)
			}
		}
		return 0, errors.New("no tw field")
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("no TCP line")
}

// Windows MIB_IF_ROW2 facts used by windowsPhysical, kept here so that the
// rule is tested on every platform.
const (
	winHardwareInterface = 1 << 0 // InterfaceAndOperStatusFlags.HardwareInterface
	winFilterInterface   = 1 << 1 // InterfaceAndOperStatusFlags.FilterInterface
	winSoftwareLoopback  = 24     // IF_TYPE_SOFTWARE_LOOPBACK
)

// windowsPhysical decides spec 010 FR-005 on Windows: a hardware interface
// that is not a filter module's view of one and not loopback. A NIC behind a
// virtual switch or a lightweight filter is therefore counted once, and
// vEthernet, VPN and tunnel adapters not at all.
func windowsPhysical(flags uint8, ifType uint32) bool {
	return flags&winHardwareInterface != 0 && flags&winFilterInterface == 0 && ifType != winSoftwareLoopback
}
