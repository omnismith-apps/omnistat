package memory_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/memory"
)

const mib = 1 << 20

// fakeReader scripts one reading per Read call; the last one repeats. The
// module takes exactly one reading per collection (FR-010), so collection n
// sees readings[n] — calls counts them so a test can pin that.
type fakeReader struct {
	readings []hostread.MemoryReading
	err      error
	calls    int
}

func (f *fakeReader) Read(context.Context) (hostread.MemoryReading, error) {
	f.calls++
	if f.err != nil {
		return hostread.MemoryReading{}, f.err
	}
	return f.readings[min(f.calls-1, len(f.readings)-1)], nil
}

func reading(total, available uint64) *fakeReader {
	return &fakeReader{readings: []hostread.MemoryReading{{Total: total, Available: available}}}
}

var errRead = errors.New("reading unavailable")

// newTestModule builds the module on a fake host and platform, logging into buf.
func newTestModule(r memory.Reader, goos string) (*memory.Module, *bytes.Buffer) {
	var buf bytes.Buffer
	return &memory.Module{
		Reader: r,
		GOOS:   goos,
		Log:    slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, &buf
}

// collectOK collects once and returns the observations keyed by manifest key.
func collectOK(t *testing.T, m *memory.Module) map[string]any {
	t.Helper()
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return byKey(t, obs)
}

func byKey(t *testing.T, obs []module.Observation) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, o := range obs {
		if _, dup := out[o.Key]; dup {
			t.Fatalf("key %q observed twice in one collection", o.Key)
		}
		out[o.Key] = o.Value
	}
	return out
}
