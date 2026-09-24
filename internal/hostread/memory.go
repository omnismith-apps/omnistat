package hostread

import (
	"context"
	"errors"
	"fmt"

	gmem "github.com/shirou/gopsutil/v4/mem"
)

// MemoryReading is one reading of physical memory, in bytes. Both fields come
// from the same call.
type MemoryReading struct {
	// Total is the physical memory the operating system reports as usable.
	Total uint64
	// Available is the operating system's own estimate of memory that can be
	// given to programs without swapping: MemAvailable on Linux, ullAvailPhys
	// on Windows.
	//
	// It is NOT maintained by the OS everywhere. On macOS gopsutil computes it
	// as free + inactive pages, ignoring compressed and purgeable memory; on
	// Linux kernels older than 3.14 (no MemAvailable) it silently substitutes
	// its own estimate. Callers gate it to Linux and Windows (spec 005 FR-011);
	// the old-kernel case is an accepted, undetectable exception (spec 005
	// edge cases).
	Available uint64
}

// Memory reads physical memory.
//
// gopsutil's Free, Used and UsedPercent are deliberately not exposed: Free is
// MemFree on Linux (it excludes reclaimable cache) and a copy of Available on
// Windows, and Used/UsedPercent are gopsutil's arithmetic, not readings.
type Memory struct{}

// Read returns the current physical memory reading. A zero Total is returned
// as read; judging it is the caller's job.
func (Memory) Read(ctx context.Context) (MemoryReading, error) {
	v, err := gmem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return MemoryReading{}, fmt.Errorf("virtual memory: %w", err)
	}
	if v == nil {
		return MemoryReading{}, errors.New("virtual memory: no reading")
	}
	return MemoryReading{Total: v.Total, Available: v.Available}, nil
}
