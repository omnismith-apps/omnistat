//go:build !linux

package systemd

// NewHost returns the systemd seam; off Linux there is none (FR-003).
func NewHost() (Host, error) { return nil, ErrUnsupported }
