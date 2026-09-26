package hostread

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// classify: Windows keeps I/O counters per volume, and gopsutil reports the
// fixed volumes that have a drive letter (spec 008 FR-011).
func classify(string) DeviceKind { return KindVolume }

// windowsDir asks the OS where Windows is installed (spec 008 FR-006), rather
// than trusting an environment variable.
func windowsDir() (string, error) {
	dir, err := windows.GetWindowsDirectory()
	if err != nil {
		return "", fmt.Errorf("windows directory: %w", err)
	}
	return dir, nil
}
