package winsvc

import (
	"context"
	"fmt"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/service"
)

// PlanUninstall decides everything `service uninstall` will do (FR-020),
// changing nothing. The configuration directory is kept, and the Omnismith
// project is never touched.
func PlanUninstall(ctx context.Context, h Host) (*service.Plan, error) {
	if !h.Elevated() {
		return nil, ErrNotElevated
	}
	progDir, err := h.ProgramDir()
	if err != nil {
		return nil, fmt.Errorf("locating the program directory: %w", err)
	}
	cfgDir, err := h.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locating the configuration directory: %w", err)
	}
	target := winJoin(progDir, BinaryName)
	inst, err := h.Service(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying the service manager: %w", err)
	}
	if !inst.Exists {
		return &service.Plan{NotInstalled: true}, nil
	}
	if err := ours(inst, target); err != nil {
		return nil, err
	}

	p := &service.Plan{Binary: target, LogHint: LogHint, Keep: []string{cfgDir + " (your configuration)"}}
	if inst.State != StateStopped {
		p.Add("stop the service (it publishes what it has buffered first)", func(ctx context.Context) (string, error) {
			return "", h.Stop(ctx)
		})
	}
	p.Add("remove service "+Name+" and its stored settings", func(ctx context.Context) (string, error) {
		return "", h.Delete(ctx)
	})
	p.Add("remove event source "+EventSource+" from the Application log", func(context.Context) (string, error) {
		return "", h.EventSource(false)
	})
	p.Add("remove "+target, func(context.Context) (string, error) {
		leftover, err := h.RemoveBinary(target)
		return leftoverNote(target, progDir, leftover), err
	})
	return p, nil
}

// leftoverNote explains a running binary that could only be moved aside (FR-020).
func leftoverNote(target, progDir, leftover string) string {
	switch {
	case leftover == "":
		return ""
	case strings.HasPrefix(strings.ToLower(leftover), strings.ToLower(progDir)+`\`):
		return fmt.Sprintf("%s is the running program: it was renamed to %s; Windows deletes it and %s at the next restart", target, leftover, progDir)
	}
	return fmt.Sprintf("%s is the running program: it was moved to %s, which Windows deletes at the next restart; %s is removed", target, leftover, progDir)
}
