package hostread

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gnet "github.com/shirou/gopsutil/v4/net"
)

// Where the Linux network readings live. They are plain files: the 007
// service sandbox allows no netlink socket (spec 010 NFR-003).
const (
	sysClassNet     = "/sys/class/net"
	procSnmp6       = "/proc/net/snmp6"
	procNetstat     = "/proc/net/netstat"
	procSockstat    = "/proc/net/sockstat"
	procConntrack   = "/proc/sys/net/netfilter/nf_conntrack_count"
	procConntrackMx = "/proc/sys/net/netfilter/nf_conntrack_max"
)

func netInterfaces(ctx context.Context) ([]IfaceCounters, error) {
	cs, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]IfaceCounters, 0, len(cs))
	for _, c := range cs {
		out = append(out, IfaceCounters{
			ID:        c.Name,
			Name:      c.Name,
			Physical:  classifyNetIn(sysClassNet, c.Name),
			RxBytes:   c.BytesRecv,
			TxBytes:   c.BytesSent,
			RxPackets: c.PacketsRecv,
			TxPackets: c.PacketsSent,
			RxErrors:  c.Errin,
			TxErrors:  c.Errout,
			RxDrops:   c.Dropin,
			TxDrops:   c.Dropout,
		})
	}
	return out, nil
}

// classifyNetIn decides spec 010 FR-005 against a /sys/class/net tree. An
// interface with a `device` link is backed by a NIC, real or presented by a
// hypervisor; loopback, bridges, veths, bonds, VLANs and tunnels have none.
// A device-backed interface whose master is device-backed too is an SR-IOV
// VF under a synthetic NIC (Azure accelerated networking), whose counters
// already include it. Bond members and bridge ports have a software master
// and stay counted.
func classifyNetIn(root, name string) bool {
	dir := filepath.Join(root, name)
	if !exists(filepath.Join(dir, "device")) {
		return false
	}
	master, err := filepath.EvalSymlinks(filepath.Join(dir, "master"))
	if err != nil {
		return true
	}
	return !exists(filepath.Join(master, "device"))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func netStack(ctx context.Context) (StackCounters, error) {
	ps, err := gnet.ProtoCountersWithContext(ctx, []string{"tcp", "udp"})
	if err != nil {
		return StackCounters{}, err
	}
	var s StackCounters
	var tcp, udp bool
	for _, p := range ps {
		switch p.Protocol {
		case "tcp":
			tcp = true
			// The TCP MIB is shared by IPv4 and IPv6 (feature 010 spike).
			for key, dst := range map[string]*uint64{
				"CurrEstab":   &s.TCPCurrEstab,
				"OutSegs":     &s.TCPOutSegs,
				"RetransSegs": &s.TCPRetransSegs,
				"OutRsts":     &s.TCPOutRsts,
			} {
				if *dst, err = stat(p.Stats, "Tcp", key); err != nil {
					return StackCounters{}, err
				}
			}
		case "udp":
			udp = true
			if s.UDPInErrors, err = stat(p.Stats, "Udp", "InErrors"); err != nil {
				return StackCounters{}, err
			}
		}
	}
	if !tcp || !udp {
		return StackCounters{}, errors.New("no Tcp or Udp line in /proc/net/snmp")
	}
	// /proc/net/snmp's Udp line is IPv4 only. With IPv6 disabled at boot
	// there is no snmp6 file, and IPv6 contributes nothing.
	v6, err := readFile(procSnmp6, parseSnmp6UDPInErrors)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return StackCounters{}, err
	default:
		s.UDPInErrors += v6
	}
	return s, nil
}

func stat(m map[string]int64, proto, key string) (uint64, error) {
	v, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("no %s %s", proto, key)
	}
	if v < 0 {
		return 0, fmt.Errorf("%s %s is negative: %d", proto, key, v)
	}
	return uint64(v), nil
}

func netListenDrops(context.Context) (uint64, error) {
	return readFile(procNetstat, parseNetstatListenDrops)
}

func netTimeWait(context.Context) (uint64, error) {
	return readFile(procSockstat, parseSockstatTimeWait)
}

func netConntrack(context.Context) (Conntrack, error) {
	count, err := readFile(procConntrack, parseUint)
	if errors.Is(err, fs.ErrNotExist) {
		return Conntrack{}, ErrNoConntrack
	}
	if err != nil {
		return Conntrack{}, err
	}
	limit, err := readFile(procConntrackMx, parseUint)
	if errors.Is(err, fs.ErrNotExist) {
		return Conntrack{}, ErrNoConntrack
	}
	if err != nil {
		return Conntrack{}, err
	}
	return Conntrack{Count: count, Max: limit}, nil
}

func parseUint(r io.Reader) (uint64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

// readFile opens path and parses it, naming the file in any error. A missing
// file keeps fs.ErrNotExist in the chain.
func readFile(path string, parse func(io.Reader) (uint64, error)) (uint64, error) {
	f, err := os.Open(path) //nolint:gosec // fixed /proc paths
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	v, err := parse(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}
