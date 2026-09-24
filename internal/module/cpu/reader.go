package cpu

import (
	"context"

	"github.com/omnismith-apps/omnistat/internal/hostread"
)

// Times is one reading of the operating system's cumulative CPU-time counters,
// aggregated over every logical CPU (spec 004 FR-005). The reading itself lives
// in hostread (ADR-0009); what counts as idle is this module's decision
// (IdleTime).
type Times = hostread.CPUTimes

// Load is the operating system's own load averages. omnistat forwards them and
// never computes them (spec 004 FR-006).
type Load = hostread.LoadAvg

// Reader is the module's view of the host: the narrow set of readings `cpu`
// needs, declared by its consumer so that tests can fake it and so that the
// dependency of ADR-0008 stays behind one interface. hostread.CPU implements
// it. Every method is a stateless read; nothing here keeps a baseline
// (ADR-0006).
type Reader interface {
	Times(ctx context.Context) (Times, error)
	LoadAvg(ctx context.Context) (Load, error)
	Counts(ctx context.Context) (int, error)
	Model(ctx context.Context) (string, error)
}

var _ Reader = hostread.CPU{}

// IdleTime is the part of t.Total() that counts as not busy: the idle counter
// plus, where the platform reports one, I/O wait (spec 004 FR-005).
func IdleTime(t Times) float64 { return t.Idle + t.Iowait }
