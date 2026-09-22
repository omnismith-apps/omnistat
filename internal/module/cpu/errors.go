package cpu

import "errors"

// errNoModel means the OS reported no model string at all (FR-009).
var errNoModel = errors.New("no model reported")
