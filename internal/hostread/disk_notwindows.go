//go:build !windows

package hostread

import (
	"errors"
	"fmt"
)

var errNotWindows = fmt.Errorf("windows directory: %w", errors.ErrUnsupported)

func windowsDir() (string, error) { return "", errNotWindows }
