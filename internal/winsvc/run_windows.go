//go:build windows

package winsvc

import (
	"context"
	"time"

	"golang.org/x/sys/windows/svc"
)

// IsService reports whether the process was started by the service manager.
func IsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// Main is the program as the service runs it: the cli with the service's
// event log and config file (FR-012, FR-025). It returns the exit code.
type Main func(ctx context.Context, events Sink, configPath string) int

// Run runs main under the service manager and returns the process exit code
// (FR-021–FR-023).
func Run(main Main) int {
	el, err := OpenEventLog()
	if err != nil {
		return 1 // nowhere to report it; the service manager records the exit code
	}
	defer el.Close()
	dir, err := configDir()
	if err != nil {
		_ = el.Error("omnistat: locating the configuration directory: " + err.Error())
		return 1
	}
	h := &handler{main: func(ctx context.Context) int { return main(ctx, el, winJoin(dir, ConfigFile)) }}
	if err := svc.Run(Name, h); err != nil {
		_ = el.Error("omnistat: service: " + err.Error())
		return 1
	}
	return h.code
}

// stopWaitHint is what omnistat tells the service manager to expect between
// progress reports while it finishes (FR-021).
const stopWaitHint uint32 = 5000 // milliseconds

// handler bridges the service manager to the daemon: a stop, shutdown or
// preshutdown request cancels its context, exactly as SIGTERM does on a
// console (003 FR-020), and progress is reported until it returns.
type handler struct {
	main func(ctx context.Context) int
	tick time.Duration // progress interval; 0 means 2s
	code int
}

func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown
	s <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- h.main(ctx) }()
	s <- svc.Status{State: svc.Running, Accepts: accepts}

	tick := h.tick
	if tick <= 0 {
		tick = 2 * time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	var checkpoint uint32
	stopping := false
	pending := func() svc.Status {
		checkpoint++
		return svc.Status{State: svc.StopPending, CheckPoint: checkpoint, WaitHint: stopWaitHint}
	}
	for {
		select {
		case code := <-done:
			h.code = code
			if code != 0 {
				// A service-specific exit code makes this a failure to Windows,
				// which then applies the recovery policy (FR-010, FR-023).
				return true, uint32(code) //nolint:gosec // exit codes are small
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown, svc.PreShutdown:
				if !stopping {
					stopping = true
					cancel()
				}
				s <- pending()
			}
		case <-ticker.C:
			if stopping {
				s <- pending()
			}
		}
	}
}
