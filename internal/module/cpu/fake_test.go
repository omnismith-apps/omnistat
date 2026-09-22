package cpu_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/cpu"
)

// fakeReader scripts every reading and records what was asked for. A non-nil
// error field makes that one reading fail, which is how the partial-collection
// rules (FR-015/016) are exercised.
type fakeReader struct {
	times    []cpu.Times // consumed in order; the last one repeats
	timesN   int
	load     cpu.Load
	counts   int
	model    string
	timesErr error
	loadErr  error
	countErr error
	modelErr error
}

func fake() *fakeReader {
	return &fakeReader{
		times:  []cpu.Times{{User: 100, Idle: 900}, {User: 200, Idle: 1800}},
		load:   cpu.Load{One: 1.5, Five: 1.25, Fifteen: 0.75},
		counts: 8,
		model:  "Fake CPU @ 3.00GHz",
	}
}

func (f *fakeReader) Times(context.Context) (cpu.Times, error) {
	if f.timesErr != nil {
		return cpu.Times{}, f.timesErr
	}
	t := f.times[min(f.timesN, len(f.times)-1)]
	f.timesN++
	return t, nil
}

func (f *fakeReader) LoadAvg(context.Context) (cpu.Load, error) {
	if f.loadErr != nil {
		return cpu.Load{}, f.loadErr
	}
	return f.load, nil
}

func (f *fakeReader) Counts(context.Context) (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.counts, nil
}

func (f *fakeReader) Model(context.Context) (string, error) {
	if f.modelErr != nil {
		return "", f.modelErr
	}
	return f.model, nil
}

var errRead = errors.New("reading unavailable")

// newTestModule builds a module on a fake host whose clock never really sleeps
// (NFR-005): Sleep records the call and returns at once.
func newTestModule(t *testing.T, r cpu.Reader) *cpu.Module {
	t.Helper()
	m := &cpu.Module{Reader: r, GOOS: "linux", Now: func() time.Time { return time.Unix(0, 0) }, Prime: cpu.DefaultPrime}
	m.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return m
}

// collectOK collects once and returns the observations keyed by manifest key.
func collectOK(t *testing.T, m *cpu.Module) map[string]any {
	t.Helper()
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return byKey(obs)
}

func byKey(obs []module.Observation) map[string]any {
	out := map[string]any{}
	for _, o := range obs {
		out[o.Key] = o.Value
	}
	return out
}
