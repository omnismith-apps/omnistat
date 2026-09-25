package winsvc

import (
	"fmt"
	"strings"
)

// winJoin joins a Windows directory and a file name on any build, so plans read
// the same in tests on Linux as on the host they are for.
func winJoin(dir, name string) string {
	return strings.TrimRight(dir, `\/`) + `\` + name
}

// samePath compares Windows paths: case-insensitively, ignoring quotes.
func samePath(a, b string) bool {
	return strings.EqualFold(strings.Trim(a, `"`), strings.Trim(b, `"`))
}

// ours reports whether an existing service is the one omnistat installs (FR-019).
func ours(inst Installed, target string) error {
	if inst.Exists && !samePath(inst.Binary, target) {
		return fmt.Errorf("%w (it runs %s); nothing was changed", ErrForeignService, inst.Binary)
	}
	return nil
}
