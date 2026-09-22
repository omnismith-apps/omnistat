package cpu

import "context"

// Times is one reading of the operating system's cumulative CPU-time counters,
// aggregated over every logical CPU. Units are irrelevant — only differences
// between two readings are ever used (spec 004 FR-005) — but every field must
// come from the same reading.
//
// Guest and GuestNice are deliberately absent: Linux already counts guest time
// inside User and guest-nice inside Nice, so a field for them would double-count
// in the denominator (plan §3).
type Times struct {
	User    float64
	Nice    float64
	System  float64
	Idle    float64
	Iowait  float64 // zero on platforms that do not account for it
	Irq     float64
	Softirq float64
	Steal   float64
}

// Total is the denominator of the usage fraction: all accounted CPU time.
func (t Times) Total() float64 {
	return t.User + t.Nice + t.System + t.Idle + t.Iowait + t.Irq + t.Softirq + t.Steal
}

// IdleTime is the part of Total that counts as not busy (spec 004 FR-005).
func (t Times) IdleTime() float64 { return t.Idle + t.Iowait }

// Load is the operating system's own load averages. omnistat forwards them and
// never computes them (spec 004 FR-006).
type Load struct {
	One     float64
	Five    float64
	Fifteen float64
}

// Reader is the module's view of the host: the narrow set of readings `cpu`
// needs, declared by its consumer so that tests can fake it and so that the
// dependency of ADR-0008 stays behind one interface. Every method is a
// stateless read; nothing here keeps a baseline (ADR-0006).
type Reader interface {
	Times(ctx context.Context) (Times, error)
	LoadAvg(ctx context.Context) (Load, error)
	Counts(ctx context.Context) (int, error)
	Model(ctx context.Context) (string, error)
}
