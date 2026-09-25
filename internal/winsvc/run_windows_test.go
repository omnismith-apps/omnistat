//go:build windows

package winsvc

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

// drive runs Execute with main and returns its channels and result.
func drive(t *testing.T, main func(ctx context.Context) int) (chan svc.ChangeRequest, chan svc.Status, chan [2]uint32) {
	t.Helper()
	h := &handler{main: main, tick: 10 * time.Millisecond}
	r := make(chan svc.ChangeRequest)
	s := make(chan svc.Status, 100)
	out := make(chan [2]uint32, 1)
	go func() {
		ssec, code := h.Execute(nil, r, s)
		b := uint32(0)
		if ssec {
			b = 1
		}
		out <- [2]uint32{b, code}
	}()
	waitState(t, s, svc.Running)
	return r, s, out
}

func waitState(t *testing.T, s chan svc.Status, want svc.State) svc.Status {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case st := <-s:
			if st.State == want {
				return st
			}
		case <-timeout:
			t.Fatalf("never reached state %d", want)
		}
	}
}

// Spec 006 FR-021: stop, shutdown and preshutdown cancel the daemon's context;
// progress is reported with advancing checkpoints until it returns 0 → clean stop.
func TestHandler_StopRequests(t *testing.T) {
	for _, cmd := range []svc.Cmd{svc.Stop, svc.Shutdown, svc.PreShutdown} {
		release := make(chan struct{})
		r, s, out := drive(t, func(ctx context.Context) int {
			<-ctx.Done()
			<-release // the final publish takes a while
			return 0
		})
		r <- svc.ChangeRequest{Cmd: cmd}
		first := waitState(t, s, svc.StopPending)
		second := waitState(t, s, svc.StopPending)
		if second.CheckPoint <= first.CheckPoint || first.WaitHint == 0 {
			t.Fatalf("cmd %d: checkpoints must advance: %+v %+v", cmd, first, second)
		}
		close(release)
		if res := <-out; res != [2]uint32{0, 0} {
			t.Fatalf("cmd %d: clean stop expected, got %v", cmd, res)
		}
	}
}

// FR-023: a daemon that fails reports a service-specific exit code, so
// Windows treats it as a failure and applies recovery (FR-010).
func TestHandler_FailureExitCode(t *testing.T) {
	_, _, out := drive(t, func(context.Context) int { return 1 })
	if res := <-out; res != [2]uint32{1, 1} {
		t.Fatalf("want service-specific exit 1, got %v", res)
	}
}

// Interrogate answers with the current status and does not stop anything.
func TestHandler_Interrogate(t *testing.T) {
	stop := make(chan struct{})
	r, s, out := drive(t, func(ctx context.Context) int { <-ctx.Done(); <-stop; return 0 })
	cur := svc.Status{State: svc.Running}
	r <- svc.ChangeRequest{Cmd: svc.Interrogate, CurrentStatus: cur}
	if got := waitState(t, s, svc.Running); got != cur {
		t.Fatalf("%+v", got)
	}
	r <- svc.ChangeRequest{Cmd: svc.Stop}
	close(stop)
	<-out
}
