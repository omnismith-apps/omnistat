package omni

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// transport adds a per-attempt deadline, bounded jittered retries on transient
// failures (NFR-003) and a User-Agent to every SDK request.
type transport struct {
	base      http.RoundTripper
	timeout   time.Duration
	retries   int
	userAgent string
	sleep     func(context.Context, time.Duration) error
	backoff   func(attempt int) time.Duration
}

func newTransport(base http.RoundTripper, timeout time.Duration, retries int, userAgent string) *transport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{
		base: base, timeout: timeout, retries: retries, userAgent: userAgent,
		sleep:   sleepCtx,
		backoff: defaultBackoff,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// defaultBackoff is 200ms·2^attempt with up to 50% jitter, capped at 5s.
func defaultBackoff(attempt int) time.Duration {
	d := 200 * time.Millisecond << attempt
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d + time.Duration(rand.Int64N(int64(d)/2+1)) //nolint:gosec // jitter, not security
}

func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.userAgent != "" {
		req.Header.Set("User-Agent", t.userAgent)
	}
	// A request with a body can only be retried if it can be rewound.
	canRetry := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil

	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt > 0 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
		}
		resp, err := t.attempt(req)
		switch {
		case err == nil && !retryable(resp.StatusCode):
			return resp, nil
		case err != nil && (errors.Is(err, context.Canceled) || req.Context().Err() != nil):
			return nil, err
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = &transientError{status: resp.StatusCode}
		}
		if !canRetry || attempt >= t.retries {
			if err == nil {
				return resp, nil // let the caller map the final 5xx/429
			}
			return nil, lastErr
		}
		delay := t.backoff(attempt)
		if resp != nil {
			if ra, ok := retryAfter(resp); ok {
				delay = ra
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		if err := t.sleep(req.Context(), delay); err != nil {
			return nil, err
		}
	}
}

// attempt performs one round trip under a per-attempt deadline. The deadline
// keeps running while the body is read; closing the body releases it.
func (t *transport) attempt(req *http.Request) (*http.Response, error) {
	if t.timeout <= 0 {
		return t.base.RoundTrip(req)
	}
	ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		d := time.Duration(secs) * time.Second
		if d > 30*time.Second {
			d = 30 * time.Second
		}
		return d, true
	}
	return 0, false
}

type transientError struct{ status int }

func (e *transientError) Error() string { return "transient HTTP " + strconv.Itoa(e.status) }
