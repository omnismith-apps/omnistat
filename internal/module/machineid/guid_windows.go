//go:build windows

package machineid

import "golang.org/x/sys/windows/registry"

// machineGUIDLocation names where the GUID is read from, for the absent-identity
// error; it never carries the value itself (002 FR-018).
const machineGUIDLocation = `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid`

// readMachineGUID reads the machine GUID Windows generates at installation
// (spec 006 FR-002). Every user may read it (FR-004). The 64-bit view is forced
// so a hypothetical 32-bit build could not be redirected to WOW6432Node.
func readMachineGUID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	return v, err
}
