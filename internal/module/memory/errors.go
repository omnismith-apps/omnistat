package memory

import "errors"

var (
	// errZeroTotal means the OS reported no physical memory (FR-008).
	errZeroTotal = errors.New("total is zero")
	// errAvailableExceedsTotal means a reading claimed more memory available
	// than exists; the pair is inconsistent and not published (FR-008).
	errAvailableExceedsTotal = errors.New("available exceeds total")
)
