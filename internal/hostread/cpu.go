package hostread

import (
	"context"
	"errors"
	"fmt"

	gcpu "github.com/shirou/gopsutil/v4/cpu"
	gload "github.com/shirou/gopsutil/v4/load"
)

// CPUTimes is one reading of the operating system's cumulative CPU-time
// counters, aggregated over every logical CPU. Units are irrelevant to its
// consumers — only differences between two readings are used (spec 004
// FR-005) — but every field comes from the same reading.
//
// Guest and GuestNice are deliberately absent: Linux already counts guest time
// inside User and guest-nice inside Nice, so a field for them would
// double-count in Total (spec 004 plan §3). The fields are disjoint.
type CPUTimes struct {
	User    float64
	Nice    float64
	System  float64
	Idle    float64
	Iowait  float64 // zero on platforms that do not account for it
	Irq     float64
	Softirq float64
	Steal   float64
}

// Total is all accounted CPU time: the sum of the disjoint fields.
func (t CPUTimes) Total() float64 {
	return t.User + t.Nice + t.System + t.Idle + t.Iowait + t.Irq + t.Softirq + t.Steal
}

// LoadAvg is the operating system's own load averages. omnistat forwards them
// and never computes them (spec 004 FR-006).
type LoadAvg struct {
	One     float64
	Five    float64
	Fifteen float64
}

// CPU reads the CPU counters, load averages, logical CPU count and model.
type CPU struct{}

// Times returns the cumulative CPU times aggregated over every logical CPU.
func (CPU) Times(ctx context.Context) (CPUTimes, error) {
	ts, err := gcpu.TimesWithContext(ctx, false)
	if err != nil {
		return CPUTimes{}, fmt.Errorf("cpu times: %w", err)
	}
	if len(ts) == 0 {
		return CPUTimes{}, errors.New("cpu times: no aggregate reading")
	}
	t := ts[0]
	// Guest and GuestNice are not copied: Linux already counts them inside
	// User and Nice, and adding them would inflate the total.
	return CPUTimes{
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

// LoadAvg returns the operating system's own load averages.
//
// On Windows gopsutil emulates them with a background goroutine sampling
// processor queue length — a different quantity and a goroutine that outlives
// the call. Callers must gate this reading to the platforms that maintain a
// load average (spec 004 FR-018), so that path is never reached.
func (CPU) LoadAvg(ctx context.Context) (LoadAvg, error) {
	l, err := gload.AvgWithContext(ctx)
	if err != nil {
		return LoadAvg{}, fmt.Errorf("load average: %w", err)
	}
	if l == nil {
		return LoadAvg{}, errors.New("load average: no reading")
	}
	return LoadAvg{One: l.Load1, Five: l.Load5, Fifteen: l.Load15}, nil
}

// Counts returns the number of logical CPUs.
func (CPU) Counts(ctx context.Context) (int, error) {
	n, err := gcpu.CountsWithContext(ctx, true)
	if err != nil {
		return 0, fmt.Errorf("cpu count: %w", err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("cpu count: implausible value %d", n)
	}
	return n, nil
}

// ErrNoModel means the OS reported no CPU model string at all.
var ErrNoModel = errors.New("no model reported")

// Model returns the model string of the first CPU the OS reports.
func (CPU) Model(ctx context.Context) (string, error) {
	info, err := gcpu.InfoWithContext(ctx)
	if err != nil {
		return "", fmt.Errorf("cpu info: %w", err)
	}
	if len(info) == 0 {
		return "", ErrNoModel
	}
	return info[0].ModelName, nil
}
