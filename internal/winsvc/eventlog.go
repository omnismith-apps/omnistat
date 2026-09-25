package winsvc

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"unicode/utf8"
)

// EventSource is the Application-log source omnistat writes as (FR-025, FR-026).
const EventSource = "omnistat"

// MaxEventLen bounds one event's text: the event log rejects strings of more
// than 31 839 characters, and a byte bound is stricter than a character one.
const MaxEventLen = 31000

// TruncatedMarker ends an event whose text was cut to MaxEventLen.
const TruncatedMarker = " …[truncated]"

// Sink receives one formatted record per call, by severity (FR-025).
type Sink interface {
	Info(msg string) error
	Warning(msg string) error
	Error(msg string) error
}

// NewEventHandler returns a slog.Handler that formats each record exactly like
// a console run (text or json) and hands it to sink by severity: error → Error,
// warn → Warning, info and debug → Information (FR-025). replace is passed to
// the underlying handler as ReplaceAttr; nil in production.
func NewEventHandler(sink Sink, level slog.Leveler, format string, replace func([]string, slog.Attr) slog.Attr) slog.Handler {
	sh := &shared{sink: sink}
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: replace}
	var inner slog.Handler = slog.NewTextHandler(&sh.buf, opts)
	if format == "json" {
		inner = slog.NewJSONHandler(&sh.buf, opts)
	}
	return &eventHandler{sh: sh, inner: inner}
}

// shared is the buffer and lock every derived handler (With*) formats into.
type shared struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	sink Sink
}

type eventHandler struct {
	sh    *shared
	inner slog.Handler
}

func (h *eventHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *eventHandler) Handle(ctx context.Context, r slog.Record) error {
	h.sh.mu.Lock()
	defer h.sh.mu.Unlock()
	h.sh.buf.Reset()
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}
	msg := truncate(strings.TrimSuffix(h.sh.buf.String(), "\n"))
	switch {
	case r.Level >= slog.LevelError:
		return h.sh.sink.Error(msg)
	case r.Level >= slog.LevelWarn:
		return h.sh.sink.Warning(msg)
	default:
		return h.sh.sink.Info(msg)
	}
}

func (h *eventHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &eventHandler{sh: h.sh, inner: h.inner.WithAttrs(as)}
}

func (h *eventHandler) WithGroup(name string) slog.Handler {
	return &eventHandler{sh: h.sh, inner: h.inner.WithGroup(name)}
}

func truncate(s string) string {
	if len(s) <= MaxEventLen {
		return s
	}
	cut := MaxEventLen - len(TruncatedMarker)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + TruncatedMarker
}

// LineWriter adapts output meant for stderr (fail() messages, errors before the
// logger exists) to one event per non-empty line (FR-025).
func LineWriter(emit func(string) error) io.Writer { return lineWriter(emit) }

type lineWriter func(string) error

func (w lineWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(string(p), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := w(truncate(line)); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}
