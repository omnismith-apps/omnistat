package network

import (
	"context"

	"github.com/omnismith-apps/omnistat/internal/hostread"
)

// Reader is the module's view of the host, declared by its consumer so that
// tests can fake it (spec 010 NFR-004). hostread.Net implements it; every
// method is a stateless read, and the baselines live in the Module (ADR-0006,
// ADR-0008, ADR-0009).
type Reader interface {
	// Interfaces reads every interface's cumulative counters, classified.
	Interfaces(ctx context.Context) ([]hostread.IfaceCounters, error)
	// Stack reads the TCP and UDP counters, IPv4 and IPv6 together.
	Stack(ctx context.Context) (hostread.StackCounters, error)
	// ListenDrops reads the cumulative listen-drop count; Linux only.
	ListenDrops(ctx context.Context) (uint64, error)
	// TimeWait reads the number of TIME_WAIT sockets; Linux only.
	TimeWait(ctx context.Context) (uint64, error)
	// Conntrack reads the connection-tracking table's size and limit; Linux
	// only. hostread.ErrNoConntrack means it is not loaded.
	Conntrack(ctx context.Context) (hostread.Conntrack, error)
}

var _ Reader = hostread.Net{}
