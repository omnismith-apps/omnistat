package disk

import (
	"context"

	"github.com/omnismith-apps/omnistat/internal/hostread"
)

// Reader is the module's view of the host, declared by its consumer so that
// tests can fake it (spec 008 NFR-004). hostread.Disk implements it; every
// method is a stateless read, and the I/O baseline lives in the Module
// (ADR-0006, ADR-0008, ADR-0009).
type Reader interface {
	// Usage reads the filesystem mounted at path.
	Usage(ctx context.Context, path string) (hostread.VolumeUsage, error)
	// Counters reads every block device's cumulative I/O counters, with
	// its kind.
	Counters(ctx context.Context) ([]hostread.DiskCounters, error)
	// WindowsDir is where Windows is installed; only called on Windows.
	WindowsDir(ctx context.Context) (string, error)
	// DirExists reports whether a directory exists; only called on macOS.
	DirExists(path string) bool
}

var _ Reader = hostread.Disk{}
