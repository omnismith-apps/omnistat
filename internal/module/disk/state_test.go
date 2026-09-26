package disk

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
)

// churnReader reports a different set of device names on every call, as a
// host with endlessly hot-plugged disks would.
type churnReader struct{ n int }

func (c *churnReader) Usage(context.Context, string) (hostread.VolumeUsage, error) {
	return hostread.VolumeUsage{Total: 1 << 30, Used: 1, Available: 1}, nil
}

func (c *churnReader) Counters(context.Context) ([]hostread.DiskCounters, error) {
	c.n++
	return []hostread.DiskCounters{
		{Name: "sda", Kind: hostread.KindDisk},
		{Name: fmt.Sprintf("usb%d", c.n), Kind: hostread.KindDisk},
		{Name: fmt.Sprintf("sda%d", c.n), Kind: hostread.KindPartition},
	}, nil
}

func (c *churnReader) WindowsDir(context.Context) (string, error) { return "", nil }
func (c *churnReader) DirExists(string) bool                      { return false }

// NFR-002: the provider retains exactly one reading of the counted devices
// present at the last collection; device churn never makes it grow.
func TestIO_StateDoesNotGrow(t *testing.T) {
	now := time.Unix(0, 0)
	m := &Module{
		Reader: &churnReader{},
		GOOS:   "linux",
		Now:    func() time.Time { return now },
		Sleep:  func(context.Context, time.Duration) error { return nil },
	}
	for range 100 {
		now = now.Add(30 * time.Second)
		if _, err := m.Collect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(m.last.dev); got != 2 {
		t.Fatalf("retained %d devices, want the 2 counted ones of the last reading", got)
	}
}
