package winsvc_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// recSink records events by severity.
type recSink struct{ events []event }

type event struct{ sev, msg string }

func (s *recSink) Info(m string) error { s.events = append(s.events, event{"info", m}); return nil }
func (s *recSink) Warning(m string) error {
	s.events = append(s.events, event{"warning", m})
	return nil
}
func (s *recSink) Error(m string) error { s.events = append(s.events, event{"error", m}); return nil }

// consoleLine renders what a console run prints for the same record (003 FR-026).
func consoleLine(t *testing.T, format string, level slog.Level, emit func(*slog.Logger)) string {
	t.Helper()
	var b bytes.Buffer
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: dropTime}
	var h slog.Handler = slog.NewTextHandler(&b, opts)
	if format == "json" {
		h = slog.NewJSONHandler(&b, opts)
	}
	emit(slog.New(h))
	return strings.TrimSuffix(b.String(), "\n")
}

func dropTime(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

// Spec 006 FR-025: severity mapping, and the event text is the console line.
func TestEventHandler_SeverityAndBody(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			sink := &recSink{}
			log := slog.New(winsvc.NewEventHandler(sink, slog.LevelDebug, format, dropTime))
			emit := func(l *slog.Logger) {
				l.Debug("collected", "module", "cpu", "value", 12.5)
				l.Info("published", "dimensions", 3, "observations", 1)
				l.Warn("publish retry", "attempt", 2)
				l.Error("stopped", "err", "token revoked")
			}
			emit(log)
			wantSev := []string{"info", "info", "warning", "error"}
			if len(sink.events) != len(wantSev) {
				t.Fatalf("events: %+v", sink.events)
			}
			lines := strings.Split(consoleLine(t, format, slog.LevelDebug, emit), "\n")
			for i, ev := range sink.events {
				if ev.sev != wantSev[i] {
					t.Errorf("event %d severity %s, want %s", i, ev.sev, wantSev[i])
				}
				if ev.msg != lines[i] {
					t.Errorf("event %d body\n got %q\nwant %q", i, ev.msg, lines[i])
				}
			}
		})
	}
}

// The configured level still filters (US-4/3: debug only with log.level debug).
func TestEventHandler_Level(t *testing.T) {
	sink := &recSink{}
	log := slog.New(winsvc.NewEventHandler(sink, slog.LevelInfo, "text", nil))
	log.Debug("hidden")
	log.Info("shown")
	if len(sink.events) != 1 || !strings.Contains(sink.events[0].msg, "shown") {
		t.Fatalf("events: %+v", sink.events)
	}
}

// Attributes and groups added through With* reach the event text.
func TestEventHandler_WithAttrsAndGroups(t *testing.T) {
	sink := &recSink{}
	log := slog.New(winsvc.NewEventHandler(sink, slog.LevelInfo, "text", nil)).With("module", "memory").WithGroup("g")
	log.InfoContext(context.Background(), "tick", "n", 1)
	if len(sink.events) != 1 || !strings.Contains(sink.events[0].msg, "module=memory") || !strings.Contains(sink.events[0].msg, "g.n=1") {
		t.Fatalf("events: %+v", sink.events)
	}
}

// An event longer than the event log accepts is truncated with a marker.
func TestEventHandler_Truncates(t *testing.T) {
	sink := &recSink{}
	log := slog.New(winsvc.NewEventHandler(sink, slog.LevelInfo, "text", nil))
	log.Info(strings.Repeat("é", winsvc.MaxEventLen))
	got := sink.events[0].msg
	if len(got) > winsvc.MaxEventLen || !strings.HasSuffix(got, winsvc.TruncatedMarker) {
		t.Fatalf("len %d, suffix %q", len(got), got[len(got)-20:])
	}
	if !strings.HasPrefix(got, "time=") && !strings.HasPrefix(got, "level=") {
		t.Fatalf("body should start like a console line: %q", got[:20])
	}
}

// Timestamps stay in the text: Event Viewer's own time is the write time, the
// record's is when it happened.
func TestEventHandler_KeepsTimeByDefault(t *testing.T) {
	sink := &recSink{}
	slog.New(winsvc.NewEventHandler(sink, slog.LevelInfo, "text", nil)).Info("x")
	if !strings.HasPrefix(sink.events[0].msg, "time="+time.Now().Format("2006-01-02")) {
		t.Fatalf("%q", sink.events[0].msg)
	}
}

// LineWriter turns stderr output (fail() messages, pre-config errors) into one
// event per non-empty line (FR-025).
func TestLineWriter(t *testing.T) {
	var got []string
	w := winsvc.LineWriter(func(s string) error { got = append(got, s); return nil })
	n, err := w.Write([]byte("omnistat: config: bad\n\nsecond line\r\nno newline"))
	if err != nil || n != len("omnistat: config: bad\n\nsecond line\r\nno newline") {
		t.Fatalf("write: %d %v", n, err)
	}
	want := []string{"omnistat: config: bad", "second line", "no newline"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}
