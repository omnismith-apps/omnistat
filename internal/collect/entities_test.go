package collect_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/moduletest"
)

func gadgetSource(t *testing.T, obs func() []module.Observation) (collect.Source, *bytes.Buffer, *slog.Logger) {
	t.Helper()
	g := moduletest.WithProvider(moduletest.Gadget(), 0, func(context.Context) ([]module.Observation, error) { return obs(), nil })
	srcs, _, err := collect.Sources(desiredFor(t, moduletest.Ident(), g), []module.Module{g}, nil, "linux")
	if err != nil || len(srcs) != 1 {
		t.Fatalf("sources: %v %v", srcs, err)
	}
	var logw bytes.Buffer
	return srcs[0], &logw, slog.New(slog.NewTextHandler(&logw, nil))
}

// spec 011 FR-006, FR-016: observations go to the host or to their entity's
// own buffer; the host link is not something the provider collects.
func TestOnce_RoutesToEntities(t *testing.T) {
	ups := module.Entity{Template: "gadget", Key: "SN-1"}
	src, logw, log := gadgetSource(t, func() []module.Observation {
		return []module.Observation{
			{Key: "count", Value: 2},
			{Key: "name", Value: "first", Entity: ups},
			{Key: "level", Value: 50, Entity: ups},
			{Key: "level", Value: 70, Entity: module.Entity{Template: "gadget", Key: "SN-2"}},
		}
	})
	if src.Links["host"] != true || src.Attrs["host"].Slug != "" {
		t.Fatalf("host link must be a link, not an attribute to collect: %+v / %+v", src.Links, src.Attrs["host"])
	}
	bufs := collect.NewBuffers(10, log)
	if failed := collect.Once(context.Background(), []collect.Source{src}, bufs, nil, log); len(failed) != 0 {
		t.Fatalf("failed: %v\n%s", failed, logw)
	}
	if host := bufs.Host().Snapshot(); len(host.Dims) != 1 || host.Dims[0].Slug != "gadget_count" {
		t.Fatalf("host batch: %+v", host)
	}
	targets := bufs.Targets()
	want := []collect.Target{{Module: "gadget", Template: "gadget", Key: "SN-1"}, {Module: "gadget", Template: "gadget", Key: "SN-2"}}
	if fmt.Sprint(targets) != fmt.Sprint(want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
	b1 := bufs.For(want[0]).Snapshot()
	if len(b1.Dims) != 1 || len(b1.Metrics) != 1 || b1.Metrics[0].Target != want[0] {
		t.Fatalf("SN-1 batch: %+v", b1)
	}
	if bufs.Empty() {
		t.Fatal("Empty with pending samples")
	}
}

// spec 011 FR-006…FR-008: wrong entity, a host link, and invalid keys are
// dropped; invalid keys cost one record per collection.
func TestOnce_DropsMisaddressedObservations(t *testing.T) {
	src, logw, log := gadgetSource(t, func() []module.Observation {
		return []module.Observation{
			{Key: "name", Value: "no entity"},
			{Key: "count", Value: 1, Entity: module.Entity{Template: "gadget", Key: "SN-1"}},
			{Key: "name", Value: "wrong template", Entity: module.Entity{Template: "widget", Key: "SN-1"}},
			{Key: "host", Value: "someone", Entity: module.Entity{Template: "gadget", Key: "SN-1"}},
			{Key: "name", Value: "x", Entity: module.Entity{Template: "gadget", Key: " padded"}},
			{Key: "level", Value: 1, Entity: module.Entity{Template: "gadget", Key: strings.Repeat("k", 200)}},
		}
	})
	bufs := collect.NewBuffers(10, log)
	collect.Once(context.Background(), []collect.Source{src}, bufs, nil, log)
	if !bufs.Empty() {
		t.Fatalf("misaddressed observations were buffered: host %+v, targets %v", bufs.Host().Snapshot(), bufs.Targets())
	}
	out := logw.String()
	for _, want := range []string{"names no entity", "the host", "widget", "host link is set by the core"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "invalid entity key"); n != 1 {
		t.Errorf("invalid keys logged %d times, want once:\n%s", n, out)
	}
	if strings.Contains(out, strings.Repeat("k", 129)) {
		t.Error("an over-long key was logged in full")
	}
}

// spec 011 FR-009: at most MaxKeysPerModule entities per module; one warning.
func TestBuffers_KeyCap(t *testing.T) {
	var logw bytes.Buffer
	bufs := collect.NewBuffers(10, slog.New(slog.NewTextHandler(&logw, nil)))
	for i := range collect.MaxKeysPerModule + 5 {
		bufs.Add(collect.Sample{Slug: "gadget_level_pct", Kind: "metric", Value: 1.0, Target: collect.Target{Module: "gadget", Template: "gadget", Key: fmt.Sprint(i)}})
	}
	bufs.Add(collect.Sample{Slug: "gadget_level_pct", Kind: "metric", Value: 2.0, Target: collect.Target{Module: "gadget", Template: "gadget", Key: "0"}})
	if n := len(bufs.Targets()); n != collect.MaxKeysPerModule {
		t.Fatalf("targets = %d", n)
	}
	if got := len(bufs.For(collect.Target{Module: "gadget", Template: "gadget", Key: "0"}).Snapshot().Metrics); got != 2 {
		t.Fatalf("a known key must keep accepting samples, has %d", got)
	}
	if n := strings.Count(logw.String(), "too many entities"); n != 1 {
		t.Fatalf("cap warning logged %d times:\n%s", n, logw.String())
	}
	bufs.Add(collect.Sample{Slug: "x", Kind: "metric", Value: 1.0, Target: collect.Target{Module: "other", Template: "t", Key: "a"}})
	if bufs.For(collect.Target{Module: "other", Template: "t", Key: "a"}) == nil {
		t.Fatal("the cap is per module")
	}
}

// spec 011 FR-006, ADR-0007: a module whose only collectable-everywhere
// attribute is its host link is still skipped where nothing else is
// collectable.
func TestSources_HostLinkDoesNotMakeAModuleCollectable(t *testing.T) {
	g := moduletest.Gadget().Manifest()
	for i := range g.Attributes {
		g.Attributes[i].Platforms = []string{"linux"}
	}
	g.Attributes[0].Platforms = nil
	m := moduletest.WithProvider(module.Static{M: g}, 0, nil)
	srcs, skipped, err := collect.Sources(desiredFor(t, moduletest.Ident(), m), []module.Module{m}, nil, "darwin")
	if err != nil || len(srcs) != 0 || len(skipped) != 1 || skipped[0].Key != "" {
		t.Fatalf("sources %v skipped %+v err %v", srcs, skipped, err)
	}
}
