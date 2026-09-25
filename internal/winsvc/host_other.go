//go:build !windows

package winsvc

// NewHost returns the Windows service manager seam; off Windows there is none
// (spec 006 FR-027).
func NewHost() (Host, error) { return nil, ErrUnsupported }
