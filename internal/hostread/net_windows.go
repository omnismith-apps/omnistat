package hostread

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The IP Helper calls that x/sys/windows does not wrap. gopsutil is not used
// for the network on Windows: its interface counters include unicast packets
// only and cannot be classified, and its protocol counters are unimplemented
// (feature 010 spike). The DLL is loaded once per process; that is a library
// handle, not a measurement baseline (ADR-0008).
var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetIfTable2         = iphlpapi.NewProc("GetIfTable2")
	procGetTCPStatisticsEx2 = iphlpapi.NewProc("GetTcpStatisticsEx2")
	procGetTCPStatisticsEx  = iphlpapi.NewProc("GetTcpStatisticsEx")
	procGetUDPStatisticsEx  = iphlpapi.NewProc("GetUdpStatisticsEx")
)

// mibIfTable2 is MIB_IF_TABLE2: a count, then the rows. MIB_IF_ROW2 starts
// with a 64-bit LUID, so the rows begin 8 bytes in.
type mibIfTable2 struct {
	NumEntries uint32
	Table      [1]windows.MibIfRow2
}

// mibTCPStats is MIB_TCPSTATS (GetTcpStatisticsEx): 15 DWORDs.
type mibTCPStats struct {
	RtoAlgorithm, RtoMin, RtoMax, MaxConn              uint32
	ActiveOpens, PassiveOpens, AttemptFails, EstabRsts uint32
	CurrEstab, InSegs, OutSegs, RetransSegs            uint32
	InErrs, OutRsts, NumConns                          uint32
}

// mibTCPStats2 is MIB_TCPSTATS2 (GetTcpStatisticsEx2, Windows 10 1709 and
// Server 2016): the segment counters are 64-bit, the rest stay 32-bit.
type mibTCPStats2 struct {
	RtoAlgorithm, RtoMin, RtoMax, MaxConn              uint32
	ActiveOpens, PassiveOpens, AttemptFails, EstabRsts uint32
	CurrEstab                                          uint32
	InSegs, OutSegs                                    uint64
	RetransSegs, InErrs, OutRsts, NumConns             uint32
}

// mibUDPStats is MIB_UDPSTATS (GetUdpStatisticsEx): 5 DWORDs.
type mibUDPStats struct {
	InDatagrams, NoPorts, InErrors, OutDatagrams, NumAddrs uint32
}

func netInterfaces(context.Context) ([]IfaceCounters, error) {
	if err := procGetIfTable2.Find(); err != nil {
		return nil, err
	}
	var table *mibIfTable2
	// GetIfTable2 allocates the table and stores its address in table.
	if r, _, _ := procGetIfTable2.Call(uintptr(unsafe.Pointer(&table))); r != 0 { //nolint:gosec // Win32 out-parameter
		return nil, fmt.Errorf("GetIfTable2: %w", windows.Errno(r))
	}
	defer windows.FreeMibTable(unsafe.Pointer(table)) //nolint:gosec // frees what GetIfTable2 allocated
	// MIB_IF_TABLE2 is a C flexible array: NumEntries rows from Table[0].
	rows := unsafe.Slice(&table.Table[0], table.NumEntries) //nolint:gosec // bounded by NumEntries
	out := make([]IfaceCounters, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		out = append(out, IfaceCounters{
			ID:        fmt.Sprintf("%016x", row.InterfaceLuid),
			Name:      windows.UTF16ToString(row.Alias[:]),
			Physical:  windowsPhysical(row.InterfaceAndOperStatusFlags, row.Type),
			RxBytes:   row.InOctets,
			TxBytes:   row.OutOctets,
			RxPackets: row.InUcastPkts + row.InNUcastPkts,
			TxPackets: row.OutUcastPkts + row.OutNUcastPkts,
			RxErrors:  row.InErrors,
			TxErrors:  row.OutErrors,
			RxDrops:   row.InDiscards,
			TxDrops:   row.OutDiscards,
		})
	}
	return out, nil
}

// families are summed (spec 010 FR-010). A family the OS does not support
// (IPv6 unbound everywhere) contributes nothing.
var families = []uint32{windows.AF_INET, windows.AF_INET6}

func netStack(context.Context) (StackCounters, error) {
	var s StackCounters
	ex2 := procGetTCPStatisticsEx2.Find() == nil
	for _, fam := range families {
		if ex2 {
			var t mibTCPStats2
			if err := statCall(procGetTCPStatisticsEx2, &t, fam); err != nil {
				if errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
					continue
				}
				return StackCounters{}, fmt.Errorf("GetTcpStatisticsEx2: %w", err)
			}
			s.TCPCurrEstab += uint64(t.CurrEstab)
			s.TCPOutSegs += t.OutSegs
			s.TCPRetransSegs += uint64(t.RetransSegs)
			s.TCPOutRsts += uint64(t.OutRsts)
			continue
		}
		var t mibTCPStats
		if err := statCall(procGetTCPStatisticsEx, &t, fam); err != nil {
			if errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
				continue
			}
			return StackCounters{}, fmt.Errorf("GetTcpStatisticsEx: %w", err)
		}
		s.TCPCurrEstab += uint64(t.CurrEstab)
		s.TCPOutSegs += uint64(t.OutSegs)
		s.TCPRetransSegs += uint64(t.RetransSegs)
		s.TCPOutRsts += uint64(t.OutRsts)
	}
	for _, fam := range families {
		var u mibUDPStats
		if err := statCall(procGetUDPStatisticsEx, &u, fam); err != nil {
			if errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
				continue
			}
			return StackCounters{}, fmt.Errorf("GetUdpStatisticsEx: %w", err)
		}
		s.UDPInErrors += uint64(u.InErrors)
	}
	return s, nil
}

// statCall calls one of the Get*StatisticsEx functions, which take the output
// structure and the address family and return a Win32 error code. T is one of
// the MIB_*STATS* structures above, whose layout the Windows tests pin.
func statCall[T mibTCPStats | mibTCPStats2 | mibUDPStats](p *windows.LazyProc, out *T, family uint32) error {
	if err := p.Find(); err != nil {
		return err
	}
	if r, _, _ := p.Call(uintptr(unsafe.Pointer(out)), uintptr(family)); r != 0 { //nolint:gosec // Win32 out-parameter
		return windows.Errno(r)
	}
	return nil
}

// Windows keeps no listen-drop or TIME_WAIT counter and has no connection
// tracking (spec 010 FR-018): the module never asks.
func netListenDrops(context.Context) (uint64, error) { return 0, errors.ErrUnsupported }
func netTimeWait(context.Context) (uint64, error)    { return 0, errors.ErrUnsupported }
func netConntrack(context.Context) (Conntrack, error) {
	return Conntrack{}, errors.ErrUnsupported
}
