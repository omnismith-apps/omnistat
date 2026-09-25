package systemd

import (
	"fmt"
	"net"
)

// Notify sends state (for example "READY=1") to the service manager's notify
// socket, named by NOTIFY_SOCKET: a path, or an abstract name starting with
// "@", which the net package maps. Without the variable — not started by
// systemd, or not as Type=notify — it does nothing (FR-010, FR-023).
func Notify(getenv func(string) string, state string) error {
	name := getenv("NOTIFY_SOCKET")
	if name == "" {
		return nil
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("notify socket: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(state)); err != nil {
		return fmt.Errorf("notify socket: %w", err)
	}
	return nil
}
