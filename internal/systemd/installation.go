package systemd

import (
	"context"

	"github.com/omnismith-apps/omnistat/internal/service"
)

// Installation describes the installed service for `omnistat upgrade`
// (spec 009): root and systemd as for install (FR-002), a foreign unit refused
// (FR-003, 007 FR-019), and the stored settings for the proxy (FR-012).
func Installation(ctx context.Context, h Host) (service.Installation, error) {
	if err := preconditions(h); err != nil {
		return service.Installation{}, err
	}
	_, installed, err := inspect(ctx, h)
	if err != nil || !installed {
		return service.Installation{}, err
	}
	stored, err := readStored(h)
	if err != nil {
		return service.Installation{}, err
	}
	return service.Installation{Exists: true, Binary: BinaryPath, Settings: stored}, nil
}
