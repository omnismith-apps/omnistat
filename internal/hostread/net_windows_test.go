package hostread

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The IP Helper structures are declared here, not in x/sys, so their C layout
// is pinned by size and offset (spec 010 NFR-004). These run on Windows CI.
func TestWindowsStructLayouts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"MIB_TCPSTATS", unsafe.Sizeof(mibTCPStats{}), 15 * 4},
		{"MIB_TCPSTATS2", unsafe.Sizeof(mibTCPStats2{}), 72},
		{"MIB_TCPSTATS2.dw64InSegs", unsafe.Offsetof(mibTCPStats2{}.InSegs), 40},
		{"MIB_TCPSTATS2.dwRetransSegs", unsafe.Offsetof(mibTCPStats2{}.RetransSegs), 56},
		{"MIB_UDPSTATS", unsafe.Sizeof(mibUDPStats{}), 5 * 4},
		{"MIB_IF_TABLE2.Table", unsafe.Offsetof(mibIfTable2{}.Table), 8},
		{"MIB_IF_ROW2", unsafe.Sizeof(windows.MibIfRow2{}), 1352},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: %d bytes, want %d", tc.name, tc.got, tc.want)
		}
	}
}
