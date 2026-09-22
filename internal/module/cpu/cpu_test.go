package cpu_test

import (
	"bytes"
	"context"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/cpu"
)

// FR-001: the manifest is the module's contract with the project schema, so it
// is pinned here attribute by attribute. Changing a default slug is a breaking
// change that needs an ADR (001 FR-002).
func TestManifest(t *testing.T) {
	m := cpu.New().Manifest()
	if m.Module != "cpu" {
		t.Fatalf("module name: %q", m.Module)
	}
	if len(m.Templates) != 0 {
		t.Fatalf("cpu attaches to the host template, declaring none of its own: %+v", m.Templates)
	}
	want := []struct {
		key, slug, name string
		kind            manifest.Kind
		platforms       string
		options         string
	}{
		{"usage", "cpu_usage_pct", "CPU usage", manifest.KindMetric, "", ""},
		{"load1", "load_avg_1", "Load average (1m)", manifest.KindMetric, "linux,darwin", ""},
		{"load5", "load_avg_5", "Load average (5m)", manifest.KindMetric, "linux,darwin", ""},
		{"load15", "load_avg_15", "Load average (15m)", manifest.KindMetric, "linux,darwin", ""},
		{"model", "cpu_model", "CPU model", manifest.KindText, "", ""},
		{"cores", "cpu_cores", "CPU cores", manifest.KindNumber, "", ""},
		{"arch", "cpu_arch", "CPU architecture", manifest.KindList, "", "amd64,arm64"},
	}
	if len(m.Attributes) != len(want) {
		t.Fatalf("got %d attributes, want %d: %+v", len(m.Attributes), len(want), m.Attributes)
	}
	for i, w := range want {
		a := m.Attributes[i]
		if a.Key != w.key || a.Slug != w.slug || a.Name != w.name || a.Kind != w.kind {
			t.Errorf("attribute %d: got %+v, want key=%s slug=%s name=%s kind=%s", i, a, w.key, w.slug, w.name, w.kind)
		}
		if got := strings.Join(a.Platforms, ","); got != w.platforms {
			t.Errorf("%s platforms: got %q, want %q", w.key, got, w.platforms)
		}
		if got := strings.Join(a.Options, ","); got != w.options {
			t.Errorf("%s options: got %q, want %q", w.key, got, w.options)
		}
		if strings.TrimSpace(a.Description) == "" {
			t.Errorf("%s: description is empty", w.key)
		}
	}
	// The manifest must satisfy the core's own rules (001 FR-004).
	if err := manifest.Validate([]manifest.Manifest{m}); err != nil {
		t.Fatalf("manifest must validate: %v", err)
	}
}

// FR-004: the default cadence is 10s — six observations per default publish.
func TestDefaultInterval(t *testing.T) {
	if got := cpu.New().DefaultInterval(); got != 10*time.Second {
		t.Fatalf("default interval: %v", got)
	}
	if _, ok := module.ProviderOf(cpu.New()); !ok {
		t.Fatal("cpu must be a provider")
	}
	if got := cpu.New().Name(); got != cpu.Name {
		t.Fatalf("Name() = %q, want %q", got, cpu.Name)
	}
}

// FR-018: load averages are declared collectable on linux and darwin only,
// because Windows maintains no load average (FR-006).
func TestLoadAveragesAreNotCollectableOnWindows(t *testing.T) {
	for _, a := range cpu.New().Manifest().Attributes {
		wantWindows := !strings.HasPrefix(a.Slug, "load_avg_")
		if got := manifest.Collectable(a.Platforms, "windows"); got != wantWindows {
			t.Errorf("%s collectable on windows = %v, want %v", a.Slug, got, wantWindows)
		}
		if !manifest.Collectable(a.Platforms, "linux") {
			t.Errorf("%s must be collectable on linux", a.Slug)
		}
	}
}

// FR-008: the architecture is the one this binary targets.
func TestCollect_Arch(t *testing.T) {
	m := newTestModule(t, fake())
	obs := collectOK(t, m)
	if got := obs["arch"]; got != runtime.GOARCH {
		t.Fatalf("arch: got %v, want %v", got, runtime.GOARCH)
	}
}

// Collect never returns a key its own manifest does not declare — the core
// would drop it (003 FR-002) and the operator would see an error per tick.
func TestCollect_OnlyDeclaredKeys(t *testing.T) {
	declared := map[string]bool{}
	for _, a := range cpu.New().Manifest().Attributes {
		declared[a.Key] = true
	}
	m := newTestModule(t, fake())
	got, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range got {
		if !declared[o.Key] {
			t.Errorf("undeclared key %q", o.Key)
		}
	}
}

// FR-006: the load averages are the operating system's own, forwarded byte for
// byte — omnistat never computes or smooths them.
func TestCollect_LoadAveragesAreForwarded(t *testing.T) {
	r := fake()
	r.load = cpu.Load{One: 3.25, Five: 2.5, Fifteen: 1.125}
	m := newTestModule(t, r)
	obs := collectOK(t, m)
	for key, want := range map[string]float64{"load1": 3.25, "load5": 2.5, "load15": 1.125} {
		if got, ok := obs[key].(float64); !ok || got != want {
			t.Errorf("%s = %v, want %v", key, obs[key], want)
		}
	}
}

// FR-006/FR-018: on a platform whose OS maintains no load average, the module
// does not collect one — and does not substitute anything for it.
func TestCollect_NoLoadAveragesOnWindows(t *testing.T) {
	m := newTestModule(t, fake())
	m.GOOS = "windows"
	obs := collectOK(t, m)
	for _, key := range []string{"load1", "load5", "load15"} {
		if v, present := obs[key]; present {
			t.Errorf("%s must not be collected on windows, got %v", key, v)
		}
	}
	// Everything the platform can report is still there.
	for _, key := range []string{"usage", "model", "cores", "arch"} {
		if _, present := obs[key]; !present {
			t.Errorf("%s must still be collected on windows", key)
		}
	}
}

// FR-009: the model is trimmed to a single line; FR-007: cores is the logical
// CPU count, as a number the core can type-check.
func TestCollect_ModelAndCores(t *testing.T) {
	r := fake()
	r.model = "  Intel(R) Core(TM) i7-1360P\nsecond line  "
	r.counts = 16
	m := newTestModule(t, r)
	obs := collectOK(t, m)
	if got := obs["model"]; got != "Intel(R) Core(TM) i7-1360P" {
		t.Fatalf("model: %q", got)
	}
	if got, ok := obs["cores"].(int); !ok || got != 16 {
		t.Fatalf("cores: %v (%T)", obs["cores"], obs["cores"])
	}
}

// FR-009: an unavailable or empty model is omitted, never published as "".
func TestCollect_EmptyModelIsOmitted(t *testing.T) {
	r := fake()
	r.model = "   "
	m := newTestModule(t, r)
	if v, present := collectOK(t, m)["model"]; present {
		t.Fatalf("an empty model must be omitted, got %q", v)
	}
}

// FR-010: the dimensions are re-observed on every collection, not only the
// first — the core's buffer keeps the latest, so this costs nothing.
func TestCollect_DimensionsOnEveryCollection(t *testing.T) {
	m := newTestModule(t, fake())
	for i := range 2 {
		obs := collectOK(t, m)
		for _, key := range []string{"model", "cores", "arch"} {
			if _, present := obs[key]; !present {
				t.Fatalf("collection %d: %s missing", i+1, key)
			}
		}
	}
}

// FR-015: a source that fails costs only the observations that depend on it.
func TestCollect_PartialFailures(t *testing.T) {
	cases := []struct {
		name    string
		break_  func(*fakeReader)
		missing []string
	}{
		{"load unavailable", func(f *fakeReader) { f.loadErr = errRead }, []string{"load1", "load5", "load15"}},
		{"model unavailable", func(f *fakeReader) { f.modelErr = errRead }, []string{"model"}},
		{"counts unavailable", func(f *fakeReader) { f.countErr = errRead }, []string{"cores"}},
		{"times unavailable", func(f *fakeReader) { f.timesErr = errRead }, []string{"usage"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := fake()
			c.break_(r)
			m := newTestModule(t, r)
			obs := collectOK(t, m)
			for _, key := range c.missing {
				if v, present := obs[key]; present {
					t.Errorf("%s should be missing, got %v", key, v)
				}
			}
			if len(obs) == 0 {
				t.Fatal("the rest of the collection must survive")
			}
			// arch never depends on the host reader at all.
			if _, present := obs["arch"]; !present {
				t.Error("arch must always be observed")
			}
		})
	}
}

// FR-015: only when nothing at all could be read is the collection a provider
// failure (003 FR-010) — and arch alone is never enough to call it a success,
// because a host that can report nothing is a broken host.
func TestCollect_EverythingFailingIsAProviderFailure(t *testing.T) {
	r := fake()
	r.timesErr, r.loadErr, r.countErr, r.modelErr = errRead, errRead, errRead, errRead
	m := newTestModule(t, r)
	if _, err := m.Collect(context.Background()); err == nil {
		t.Fatal("a collection that read nothing must fail")
	}
}

// FR-016: omissions are reported once per collection, naming the keys and the
// reason, so the operator sees one record rather than one per value.
func TestCollect_OmissionsLoggedOnce(t *testing.T) {
	r := fake()
	r.loadErr = errRead
	r.modelErr = errRead
	var buf bytes.Buffer
	m := newTestModule(t, r)
	m.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	collectOK(t, m)
	logs := buf.String()
	if n := strings.Count(logs, "observations omitted"); n != 1 {
		t.Fatalf("omissions must be reported once, got %d:\n%s", n, logs)
	}
	for _, want := range []string{"load1", "model", errRead.Error()} {
		if !strings.Contains(logs, want) {
			t.Errorf("omission record should name %q:\n%s", want, logs)
		}
	}
	// Nothing is omitted on a healthy host, so nothing is logged.
	buf.Reset()
	ok := newTestModule(t, fake())
	ok.Log = m.Log
	collectOK(t, ok)
	if strings.Contains(buf.String(), "observations omitted") {
		t.Fatalf("a complete collection must log no omissions:\n%s", buf.String())
	}
}
