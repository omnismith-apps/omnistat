package cli

import (
	"io"
	"log/slog"

	"github.com/omnismith-apps/omnistat/internal/systemd"
	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// newLogger builds the process logger (FR-028): to the Windows event log when
// the app runs as a service (spec 006 FR-025), to the journal with priorities
// under systemd (spec 007 FR-022), to w otherwise.
func (a *App) newLogger(w io.Writer, level, format string) *slog.Logger {
	if a.Events != nil {
		return slog.New(winsvc.NewEventHandler(a.Events, parseLevel(level), format, nil))
	}
	if a.Journal != nil {
		return slog.New(systemd.NewJournalHandler(a.Journal, parseLevel(level), format))
	}
	return newLogger(w, level, format)
}

// newLogger builds the stderr logger (FR-028). Callers never pass secrets as
// attributes; the token and project id are not known to any logging site.
func newLogger(w io.Writer, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
