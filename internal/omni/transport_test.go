package omni

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func noSleep(context.Context, time.Duration) error { return nil }

func newTestTransport(retries int, timeout time.Duration) (*transport, *[]time.Duration) {
	var delays []time.Duration
	tr := newTransport(http.DefaultTransport, timeout, retries, "omnistat/test")
	tr.backoff = func(attempt int) time.Duration { return time.Duration(attempt+1) * time.Millisecond }
	tr.sleep = func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }
	return tr, &delays
}

// NFR-003: transient statuses are retried with backoff, then the last response
// is returned for mapping; the request body is replayed on every attempt.
func TestTransport_RetriesTransientAndReplaysBody(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if r.Header.Get("User-Agent") != "omnistat/test" {
			t.Errorf("user agent: %q", r.Header.Get("User-Agent"))
		}
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(503)
		case 2:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
		default:
			w.WriteHeader(201)
		}
	}))
	defer srv.Close()

	tr, delays := newTestTransport(3, time.Second)
	client := &http.Client{Transport: tr}
	resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 201 || calls.Load() != 3 {
		t.Fatalf("status %d after %d calls", resp.StatusCode, calls.Load())
	}
	if len(bodies) != 3 || bodies[2] != `{"x":1}` {
		t.Fatalf("body not replayed: %q", bodies)
	}
	if len(*delays) != 2 || (*delays)[1] != time.Second {
		t.Fatalf("delays: %v (Retry-After should win)", *delays)
	}
}

func TestTransport_GivesUpAfterRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(502)
	}))
	defer srv.Close()
	tr, _ := newTestTransport(2, time.Second)
	resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 502 || calls.Load() != 3 {
		t.Fatalf("status %d after %d calls (want 502 after 3)", resp.StatusCode, calls.Load())
	}
}

func TestTransport_NoRetryOnClientError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(422)
	}))
	defer srv.Close()
	tr, _ := newTestTransport(3, time.Second)
	resp, _ := (&http.Client{Transport: tr}).Get(srv.URL)
	_ = resp.Body.Close()
	if calls.Load() != 1 {
		t.Fatalf("4xx must not be retried, got %d calls", calls.Load())
	}
}

// NFR-003: every attempt has a deadline; a hung server yields a timeout error,
// not a hang, and network errors are retried.
func TestTransport_DeadlinePerAttempt(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			<-release
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	defer close(release)
	tr, _ := newTestTransport(2, 30*time.Millisecond)
	tr.sleep = noSleep
	start := time.Now()
	resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
	if err != nil {
		t.Fatalf("expected success after two timeouts, got %v", err)
	}
	_ = resp.Body.Close()
	if calls.Load() != 3 || time.Since(start) > 2*time.Second {
		t.Fatalf("calls=%d elapsed=%s", calls.Load(), time.Since(start))
	}
}

func TestTransport_CancelledContextStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	tr, _ := newTestTransport(5, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	tr.sleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestDefaultBackoff_Bounded(t *testing.T) {
	for attempt := range 10 {
		d := defaultBackoff(attempt)
		if d < 200*time.Millisecond || d > 7500*time.Millisecond {
			t.Fatalf("attempt %d: %s out of bounds", attempt, d)
		}
	}
}
