package cpu

import "github.com/omnismith-apps/omnistat/internal/hostread"

// errNoModel means the OS reported no model string at all (FR-009).
var errNoModel = hostread.ErrNoModel
