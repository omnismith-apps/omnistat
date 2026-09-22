package cpu

import (
	"context"
	"errors"
	"fmt"

	gcpu "github.com/shirou/gopsutil/v4/cpu"
	gload "github.com/shirou/gopsutil/v4/load"
)

// gopsutilReader reads the host through gopsutil (ADR-0008). This is the only
// file in the repository that imports it, so replacing the source for one
// platform — or altogether — touches one implementation.
//
// Every call here is a stateless read. gopsutil's own cpu.Percent is
// deliberately not used: it keeps a package-level baseline seeded in an
// init(), shared by every caller in the process, which is exactly the state
// ADR-0006 puts in the provider where its lifetime is visible.
type gopsutilReader struct{}

// Times returns the cumulative CPU times aggregated over every logical CPU.
func (gopsutilReader) Times(ctx context.Context) (Times, error) {
	ts, err := gcpu.TimesWithContext(ctx, false)
	if err != nil {
		return Times{}, fmt.Errorf("cpu times: %w", err)
	}
	if len(ts) == 0 {
		return Times{}, errors.New("cpu times: no aggregate reading")
	}
	t := ts[0]
	// Guest and GuestNice are not copied: Linux already counts them inside
	// User and Nice, and adding them would inflate the denominator (plan §3).
	return Times{
		User:    t.User,
		Nice:    t.Nice,
		System:  t.System,
		Idle:    t.Idle,
		Iowait:  t.Iowait,
		Irq:     t.Irq,
		Softirq: t.Softirq,
		Steal:   t.Steal,
	}, nil
}

// LoadAvg returns the operating system's own load averages. It is only ever
// called on a platform whose manifest declares the load attributes collectable
// (FR-018), so the emulated Windows value never reaches a caller.
func (gopsutilReader) LoadAvg(ctx context.Context) (Load, error) {
	l, err := gload.AvgWithContext(ctx)
	if err != nil {
		return Load{}, fmt.Errorf("load average: %w", err)
	}
	if l == nil {
		return Load{}, errors.New("load average: no reading")
	}
	return Load{One: l.Load1, Five: l.Load5, Fifteen: l.Load15}, nil
}

// Counts returns the number of logical CPUs (FR-007).
func (gopsutilReader) Counts(ctx context.Context) (int, error) {
	n, err := gcpu.CountsWithContext(ctx, true)
	if err != nil {
		return 0, fmt.Errorf("cpu count: %w", err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("cpu count: implausible value %d", n)
	}
	return n, nil
}

// Model returns the model string of the first CPU the OS reports (FR-009).
func (gopsutilReader) Model(ctx context.Context) (string, error) {
	info, err := gcpu.InfoWithContext(ctx)
	if err != nil {
		return "", fmt.Errorf("cpu info: %w", err)
	}
	if len(info) == 0 {
		return "", errNoModel
	}
	return info[0].ModelName, nil
}
