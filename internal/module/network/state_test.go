package network

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
)

// churnReader reports a different set of interface names on every call, as a
// container host starting and stopping containers would.
type churnReader struct{ n int }

func (c *churnReader) Interfaces(context.Context) ([]hostread.IfaceCounters, error) {
	c.n++
	return []hostread.IfaceCounters{
		{ID: "eth0", Name: "eth0", Physical: true},
		{ID: fmt.Sprintf("usb%d", c.n), Name: "usb", Physical: true},
		{ID: fmt.Sprintf("veth%d", c.n), Name: "veth"},
	}, nil
}

func (c *churnReader) Stack(context.Context) (hostread.StackCounters, error) {
	return hostread.StackCounters{}, nil
}
func (c *churnReader) ListenDrops(context.Context) (uint64, error) { return 0, nil }
func (c *churnReader) TimeWait(context.Context) (uint64, error)    { return 0, nil }
func (c *churnReader) Conntrack(context.Context) (hostread.Conntrack, error) {
	return hostread.Conntrack{Max: 1}, nil
}

// NFR-002: the provider retains exactly one reading of the physical
// interfaces present at the last collection; churn never makes it grow.
func TestStateDoesNotGrow(t *testing.T) {
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
	if got := len(m.last.ifaces); got != 2 {
		t.Fatalf("retained %d interfaces, want the 2 physical ones of the last reading", got)
	}
}
