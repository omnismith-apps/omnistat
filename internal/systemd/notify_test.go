package systemd_test

import (
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/systemd"
)

// NotifyListener is a stand-in for systemd's notify socket.
func notifyListener(t *testing.T, name string) *net.UnixConn {
	t.Helper()
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func receive(t *testing.T, c *net.UnixConn) string {
	t.Helper()
	buf := make([]byte, 1024)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

// FR-010, FR-023: the state reaches the socket systemd named, a path or an
// abstract name; without one, Notify does nothing.
func TestNotify(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no datagram unix sockets on Windows; systemd is Linux-only")
	}
	names := []string{filepath.Join(t.TempDir(), "notify.sock")}
	if runtime.GOOS == "linux" {
		names = append(names, fmt.Sprintf("@omnistat-test-%d", time.Now().UnixNano()))
	}
	for _, name := range names {
		c := notifyListener(t, name)
		getenv := func(k string) string {
			if k == "NOTIFY_SOCKET" {
				return name
			}
			return ""
		}
		if err := systemd.Notify(getenv, "READY=1"); err != nil {
			t.Fatal(err)
		}
		if got := receive(t, c); got != "READY=1" {
			t.Fatalf("%s: %q", name, got)
		}
	}
	if err := systemd.Notify(func(string) string { return "" }, "READY=1"); err != nil {
		t.Fatalf("no socket is not an error: %v", err)
	}
}
