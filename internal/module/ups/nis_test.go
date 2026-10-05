package ups

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

var dialer = (&net.Dialer{}).DialContext

// spec 012 FR-009: one `status` request, every line of the reply, and the
// connection closed before returning.
func TestReadStatus_Exchange(t *testing.T) {
	srv := newFakeNIS(t, usbOnline)
	lines, err := readStatus(context.Background(), dialer, srv.addr())
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != strings.Count(usbOnline, "\n") || lines[0] != "APC      : 001,036,0879" {
		t.Fatalf("lines: %d, first %q", len(lines), lines[0])
	}
	select {
	case <-srv.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the connection was left open")
	}
	if len(srv.requests) != 1 || srv.requests[0] != "status" {
		t.Fatalf("requests: %q", srv.requests)
	}
}

// spec 012 FR-009, FR-017, FR-019: every way the exchange can fail fails the
// collection with a reason, never hangs past the deadline, and closes.
func TestReadStatus_Failures(t *testing.T) {
	refused := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		return addr
	}()
	tests := []struct {
		name, behaviour, want string
		closes                bool
	}{
		{"hung server", "hang", "deadline exceeded", true},
		{"oversized frame", "huge-frame", "exceeds 1024", true},
		{"closed early", "close-early", "before the end of its reply", true},
		{"endless reply", "endless", "exceeds 512 lines", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeNIS(t, usbOnline)
			srv.set(usbOnline, tt.behaviour)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := readStatus(ctx, dialer, srv.addr())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
			if took := time.Since(start); took > 2*time.Second {
				t.Fatalf("took %s", took)
			}
			select {
			case <-srv.closed:
			case <-time.After(2 * time.Second):
				t.Fatal("the connection was left open")
			}
		})
	}
	t.Run("refused", func(t *testing.T) {
		_, err := readStatus(context.Background(), dialer, refused)
		if err == nil || !strings.Contains(err.Error(), "refused") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		srv := newFakeNIS(t, "")
		srv.set("", "hang")
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		if _, err := readStatus(ctx, dialer, srv.addr()); err == nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("got %v", err)
		}
	})
}

// spec 012 FR-010, FR-017: apcupsd's own error text is not a status.
func TestParseStatus(t *testing.T) {
	if _, err := parseStatus([]string{"Apcupsd internal error"}); err == nil || !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("error reply: %v", err)
	}
	if _, err := parseStatus(nil); err == nil {
		t.Fatal("empty reply accepted")
	}
	if _, err := parseStatus([]string{"FOO : bar"}); err == nil || !strings.Contains(err.Error(), "not an apcupsd status") {
		t.Fatalf("foreign reply: %v", err)
	}
	st, err := parseStatus(strings.Split(strings.TrimSpace(usbOnline), "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if st["STARTTIME"] != "2026-10-01 09:12:44 +0300" || st["END APC"] != "2026-10-05 21:14:08 +0300" || st["MODEL"] != "Back-UPS XS 950U" {
		t.Fatalf("parsed: %q / %q / %q", st["STARTTIME"], st["END APC"], st["MODEL"])
	}
}
