//go:build !windows

package machineid

import "errors"

// machineGUIDLocation mirrors the Windows constant so Discover reads the same
// on every build; only Windows ever reaches it with the real reader.
const machineGUIDLocation = `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid`

// readMachineGUID exists only on Windows (spec 006 FR-002).
func readMachineGUID() (string, error) {
	return "", errors.New("machine GUID is only available on Windows")
}
