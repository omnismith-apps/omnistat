package disk

import "errors"

var (
	// errNoDrive means the Windows directory names no drive letter, so the
	// system volume cannot be named (FR-006).
	errNoDrive = errors.New("no drive letter")
	// errEmptyVolume means the volume reported no size, or nothing used and
	// nothing available (FR-010).
	errEmptyVolume = errors.New("volume reports no size")
	// errInodesInconsistent means more free inodes than inodes (FR-009).
	errInodesInconsistent = errors.New("free inodes exceed total")
	// errNoDevices means no physical disk (or Windows volume) had counters
	// in both readings, so there is no I/O to report — which is not the same
	// as none happening (FR-015).
	errNoDevices = errors.New("no disk counters to count")
)
