package systemd_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/systemd"
)

// FR-022: each record carries its journal priority, and the text after it is
// the line a console run prints, in either format, attributes included.
func TestJournalHandler_Priorities(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		var b bytes.Buffer
		log := slog.New(systemd.NewJournalHandler(&b, slog.LevelDebug, format)).With("module", "cpu")
		log.Debug("d", "n", 1)
		log.Info("i")
		log.Warn("w")
		log.Error("e", "error", "boom")
		lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
		if len(lines) != 4 {
			t.Fatalf("%s: one line per record:\n%s", format, b.String())
		}
		for i, prio := range []string{"<7>", "<6>", "<4>", "<3>"} {
			if !strings.HasPrefix(lines[i], prio) || !strings.Contains(lines[i], "cpu") {
				t.Errorf("%s: line %d should start with %s and keep attributes: %q", format, i, prio, lines[i])
			}
		}
		if format == "json" && !strings.HasPrefix(lines[3], `<3>{"time":`) {
			t.Errorf("json body unchanged after the prefix: %q", lines[3])
		}
		if format == "text" && !strings.Contains(lines[3], `level=ERROR msg=e module=cpu error=boom`) {
			t.Errorf("text body unchanged after the prefix: %q", lines[3])
		}
	}
}

// FR-022: the level filter applies before anything is written.
func TestJournalHandler_Level(t *testing.T) {
	var b bytes.Buffer
	log := slog.New(systemd.NewJournalHandler(&b, slog.LevelWarn, "text"))
	log.Info("hidden")
	log.WithGroup("g").Warn("shown", "k", "v")
	if strings.Contains(b.String(), "hidden") || !strings.HasPrefix(b.String(), "<4>") || !strings.Contains(b.String(), "g.k=v") {
		t.Fatalf("%q", b.String())
	}
}

// FR-022: plain stderr output (a fail() message) gets a priority on every line,
// however it is split across writes.
func TestPriorityWriter(t *testing.T) {
	var b bytes.Buffer
	w := systemd.PriorityWriter(&b, systemd.PrioErr)
	for _, s := range []string{"omnistat: config: ", "bad\nsecond line\n", "\n", "third"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	want := "<3>omnistat: config: bad\n<3>second line\n<3>\n<3>third"
	if b.String() != want {
		t.Fatalf("got %q\nwant %q", b.String(), want)
	}
}
