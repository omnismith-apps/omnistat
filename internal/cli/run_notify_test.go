package cli_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

// notifySocket stands in for systemd's notify socket and points the daemon's
// NOTIFY_SOCKET at it.
func notifySocket(t *testing.T, h *harness) *net.UnixConn {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no datagram unix sockets on Windows; systemd is Linux-only")
	}
	path := filepath.Join(t.TempDir(), "notify.sock")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	h.env["NOTIFY_SOCKET"] = path
	return c
}

// next returns the next datagram, or "" when none comes within wait.
func next(t *testing.T, c *net.UnixConn, wait time.Duration) string {
	t.Helper()
	buf := make([]byte, 1024)
	_ = c.SetReadDeadline(time.Now().Add(wait))
	n, err := c.Read(buf)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

// 007 FR-010, FR-023: the daemon says READY=1 once the schema is reconciled
// and the host resolved; on stop it says STOPPING=1 and extends systemd's stop
// timeout to its HTTP timeout plus 5s, which covers the final publish.
func TestDaemon_NotifiesSystemd(t *testing.T) {
	h := newHarness(t, "http:\n  timeout: 7s\n")
	c := notifySocket(t, h)
	d := h.daemon(context.Background())
	if got := next(t, c, 5*time.Second); got != "READY=1" {
		t.Fatalf("readiness: %q", got)
	}
	if h.entityID(t) == "" {
		t.Fatal("ready only after the host entity is resolved")
	}
	h.parked(t)
	if r := d.stop(t); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if got := next(t, c, 5*time.Second); got != "STOPPING=1\nEXTEND_TIMEOUT_USEC=12000000" {
		t.Fatalf("stopping: %q", got)
	}
}

// 007 FR-010: a daemon that fails to start never says it is ready, so systemd
// counts a failed start; a dry-run never says it either.
func TestDaemon_NoReadiness(t *testing.T) {
	h := newHarness(t, "")
	c := notifySocket(t, h)
	h.env["OMNISMITH_ACCESS_TOKEN"] = "omni_revoked"
	if r := <-h.daemon(context.Background()).done; r.code != 1 {
		t.Fatalf("%+v", r)
	}
	if got := next(t, c, 200*time.Millisecond); got != "" {
		t.Fatalf("a failed start must not be ready: %q", got)
	}

	h.env["OMNISMITH_ACCESS_TOKEN"] = omnitest.Token
	d := h.daemon(context.Background(), "--dry-run")
	h.parked(t)
	d.stop(t)
	if got := next(t, c, 200*time.Millisecond); got != "" {
		t.Fatalf("a dry-run is not a service: %q", got)
	}
}
