package upgrade

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// removeAtRestart schedules paths for deletion at the next restart, in order
// (a file before its directory).
func removeAtRestart(paths ...string) error {
	for _, p := range paths {
		p16, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return err
		}
		if err := windows.MoveFileEx(p16, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT); err != nil {
			return fmt.Errorf("scheduling removal of %s at restart: %w", p, err)
		}
	}
	return nil
}
