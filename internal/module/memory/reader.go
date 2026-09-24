package memory

import (
	"context"

	"github.com/omnismith-apps/omnistat/internal/hostread"
)

// Reader is the module's view of the host: one physical-memory reading,
// declared by its consumer so that tests can fake it (spec 005 NFR-004).
// hostread.Memory implements it; the read is stateless (ADR-0008, ADR-0009).
type Reader interface {
	Read(ctx context.Context) (hostread.MemoryReading, error)
}

var _ Reader = hostread.Memory{}
