package winsvc

import (
	"context"
	"fmt"

	"github.com/omnismith-apps/omnistat/internal/service"
)

// Installation describes the installed service for `omnistat upgrade`
// (spec 009): elevation as for install (FR-002), a foreign service refused
// (FR-003, 006 FR-019), and the stored settings for the proxy (FR-012).
func Installation(ctx context.Context, h Host) (service.Installation, error) {
	if !h.Elevated() {
		return service.Installation{}, ErrNotElevated
	}
	progDir, err := h.ProgramDir()
	if err != nil {
		return service.Installation{}, fmt.Errorf("locating the program directory: %w", err)
	}
	inst, err := h.Service(ctx)
	if err != nil {
		return service.Installation{}, fmt.Errorf("querying the service manager: %w", err)
	}
	if !inst.Exists {
		return service.Installation{}, nil
	}
	if err := ours(inst, winJoin(progDir, BinaryName)); err != nil {
		return service.Installation{}, err
	}
	return service.Installation{Exists: true, Binary: inst.Binary, Settings: inst.Env}, nil
}
