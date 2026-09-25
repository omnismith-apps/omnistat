package systemd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// Journal priorities (syslog levels, as journald reads a "<N>" line prefix).
const (
	PrioErr     = 3
	PrioWarning = 4
	PrioInfo    = 6
	PrioDebug   = 7
)

// priority maps a record's level to its journal priority (FR-022).
func priority(l slog.Level) int {
	switch {
	case l >= slog.LevelError:
		return PrioErr
	case l >= slog.LevelWarn:
		return PrioWarning
	case l >= slog.LevelInfo:
		return PrioInfo
	}
	return PrioDebug
}

// NewJournalHandler returns a slog.Handler that formats each record exactly
// like a console run (text or json) and writes it to w, the journal stream,
// behind its priority prefix: error → err, warn → warning, info → info,
// debug → debug (FR-022).
func NewJournalHandler(w io.Writer, level slog.Leveler, format string) slog.Handler {
	sh := &journalShared{w: w}
	opts := &slog.HandlerOptions{Level: level}
	var inner slog.Handler = slog.NewTextHandler(&sh.buf, opts)
	if format == "json" {
		inner = slog.NewJSONHandler(&sh.buf, opts)
	}
	return &journalHandler{sh: sh, inner: inner}
}

// journalShared is the buffer and lock every derived handler (With*) formats into.
type journalShared struct {
	mu  sync.Mutex
	buf bytes.Buffer
	w   io.Writer
}

type journalHandler struct {
	sh    *journalShared
	inner slog.Handler
}

func (h *journalHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *journalHandler) Handle(ctx context.Context, r slog.Record) error {
	h.sh.mu.Lock()
	defer h.sh.mu.Unlock()
	h.sh.buf.Reset()
	fmt.Fprintf(&h.sh.buf, "<%d>", priority(r.Level))
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}
	_, err := h.sh.w.Write(h.sh.buf.Bytes())
	return err
}

func (h *journalHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &journalHandler{sh: h.sh, inner: h.inner.WithAttrs(as)}
}

func (h *journalHandler) WithGroup(name string) slog.Handler {
	return &journalHandler{sh: h.sh, inner: h.inner.WithGroup(name)}
}

// PriorityWriter prefixes every line written to w with the priority prio, for
// output that is not a log record: fail() messages, usage errors (FR-022).
func PriorityWriter(w io.Writer, prio int) io.Writer {
	return &priorityWriter{w: w, prefix: fmt.Appendf(nil, "<%d>", prio), atStart: true}
}

type priorityWriter struct {
	mu      sync.Mutex
	w       io.Writer
	prefix  []byte
	atStart bool
}

func (p *priorityWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []byte
	for _, c := range b {
		if p.atStart {
			out = append(out, p.prefix...)
		}
		out = append(out, c)
		p.atStart = c == '\n'
	}
	if _, err := p.w.Write(out); err != nil {
		return 0, err
	}
	return len(b), nil
}
