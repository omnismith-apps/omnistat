package hostread

import (
	"strings"
	"testing"
)

// These parsers read Linux /proc files but are pure, so they are tested on
// every platform CI runs on. The samples are from the feature 010 spike host.

// Spec 010 FR-010: IPv6 UDP receive errors live in /proc/net/snmp6.
func TestParseSnmp6UDPInErrors(t *testing.T) {
	const sample = `Ip6InReceives                   	1234
Udp6InDatagrams                 	5678
Udp6NoPorts                     	12
Udp6InErrors                    	7
Udp6OutDatagrams                	900
Udp6RcvbufErrors                	3
`
	got, err := parseSnmp6UDPInErrors(strings.NewReader(sample))
	if err != nil || got != 7 {
		t.Fatalf("got %d, %v; want 7", got, err)
	}
	if _, err := parseSnmp6UDPInErrors(strings.NewReader("Udp6InDatagrams 1\n")); err == nil {
		t.Fatal("missing Udp6InErrors: want an error")
	}
	if _, err := parseSnmp6UDPInErrors(strings.NewReader("Udp6InErrors x\n")); err == nil {
		t.Fatal("unparsable value: want an error")
	}
}

// Spec 010 FR-013: listen drops are TcpExt's ListenDrops, found by column
// name, so a kernel that adds columns does not shift it.
func TestParseNetstatListenDrops(t *testing.T) {
	const sample = `TcpExt: SyncookiesSent SyncookiesRecv ListenOverflows ListenDrops TCPTimeouts
TcpExt: 0 0 36 36 33664
IpExt: InNoRoutes InTruncatedPkts
IpExt: 0 0
`
	got, err := parseNetstatListenDrops(strings.NewReader(sample))
	if err != nil || got != 36 {
		t.Fatalf("got %d, %v; want 36", got, err)
	}
	const extra = `TcpExt: NewColumn ListenDrops Another
TcpExt: 9 41 1
`
	if got, err := parseNetstatListenDrops(strings.NewReader(extra)); err != nil || got != 41 {
		t.Fatalf("extra columns: got %d, %v; want 41", got, err)
	}
	for name, bad := range map[string]string{
		"no TcpExt":     "IpExt: InNoRoutes\nIpExt: 0\n",
		"no column":     "TcpExt: ListenOverflows\nTcpExt: 1\n",
		"no value line": "TcpExt: ListenDrops\n",
		"short line":    "TcpExt: A ListenDrops\nTcpExt: 1\n",
		"not a number":  "TcpExt: ListenDrops\nTcpExt: x\n",
	} {
		if _, err := parseNetstatListenDrops(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// Spec 010 FR-011: TIME_WAIT is the `tw` field of sockstat's TCP line, which
// covers IPv4 and IPv6 (spike).
func TestParseSockstatTimeWait(t *testing.T) {
	const sample = `sockets: used 1822
TCP: inuse 44 orphan 0 tw 32 alloc 160 mem 891
UDP: inuse 26 mem 1966
`
	got, err := parseSockstatTimeWait(strings.NewReader(sample))
	if err != nil || got != 32 {
		t.Fatalf("got %d, %v; want 32", got, err)
	}
	for name, bad := range map[string]string{
		"no TCP line":  "sockets: used 1\nUDP: inuse 1 mem 1\n",
		"no tw field":  "TCP: inuse 44 orphan 0\n",
		"tw at end":    "TCP: inuse 44 tw\n",
		"not a number": "TCP: tw x\n",
	} {
		if _, err := parseSockstatTimeWait(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// Spec 010 FR-005: on Windows a physical interface is a hardware interface
// that is neither a filter module's interface nor loopback.
func TestWindowsPhysical(t *testing.T) {
	const (
		hardware = 1 << 0
		filter   = 1 << 1
		conn     = 1 << 2
		ethernet = 6
		wifi     = 71
		loopback = 24
		tunnel   = 131
	)
	for _, tc := range []struct {
		name   string
		flags  uint8
		ifType uint32
		want   bool
	}{
		{"ethernet NIC", hardware | conn, ethernet, true},
		{"wifi NIC", hardware | conn, wifi, true},
		{"filter over the NIC", hardware | filter, ethernet, false},
		{"vEthernet / VPN adapter", 0, ethernet, false},
		{"loopback", 0, loopback, false},
		{"loopback flagged hardware", hardware, loopback, false},
		{"ISATAP / Teredo", 0, tunnel, false},
	} {
		if got := windowsPhysical(tc.flags, tc.ifType); got != tc.want {
			t.Errorf("%s: windowsPhysical(%#x, %d) = %v, want %v", tc.name, tc.flags, tc.ifType, got, tc.want)
		}
	}
}
