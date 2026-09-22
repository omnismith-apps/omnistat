package collect_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/manifest"
)

func at(sec int) time.Time { return time.Date(2026, 9, 22, 10, 0, sec, 0, time.UTC) }

func dim(slug, v string, sec int) collect.Sample {
	return collect.Sample{Module: "m", Key: slug, Slug: slug, Kind: manifest.KindText, Value: v, At: at(sec)}
}

func metric(slug string, v float64, sec int) collect.Sample {
	return collect.Sample{Module: "m", Key: slug, Slug: slug, Kind: manifest.KindMetric, Value: v, At: at(sec)}
}

// FR-007: dimensions keep the latest per attribute; metrics keep everything in order.
func TestBuffer_LatestDimAllMetrics(t *testing.T) {
	b := collect.NewBuffer(10)
	b.Add(dim("hostname", "a", 1))
	b.Add(dim("hostname", "b", 2))
	b.Add(dim("arch", "amd64", 1))
	b.Add(metric("cpu", 1, 1))
	b.Add(metric("cpu", 2, 2))
	b.Add(metric("mem", 3, 1))

	if b.Empty() {
		t.Fatal("not empty")
	}
	s := b.Snapshot()
	if len(s.Dims) != 2 || s.Dims[0].Slug != "arch" || s.Dims[1].Slug != "hostname" || s.Dims[1].Value != "b" {
		t.Fatalf("dims: %+v", s.Dims)
	}
	if len(s.Metrics) != 3 || s.Metrics[0].Slug != "cpu" || s.Metrics[0].Value != 1.0 || s.Metrics[1].Value != 2.0 || s.Metrics[2].Slug != "mem" {
		t.Fatalf("metrics: %+v", s.Metrics)
	}
	if collect.NewBuffer(10).Snapshot().Empty() != true {
		t.Fatal("empty snapshot")
	}
}

// FR-008: bounded per metric attribute, oldest dropped, drops counted once.
func TestBuffer_Bound(t *testing.T) {
	b := collect.NewBuffer(3)
	for i := 1; i <= 5; i++ {
		b.Add(metric("cpu", float64(i), i))
	}
	b.Add(metric("mem", 1, 1))
	s := b.Snapshot()
	if len(s.Metrics) != 4 || s.Metrics[0].Value != 3.0 || s.Metrics[2].Value != 5.0 {
		t.Fatalf("metrics after drop: %+v", s.Metrics)
	}
	if d := b.Drops(); len(d) != 1 || d["cpu"] != 2 {
		t.Fatalf("drops: %v", d)
	}
	if d := b.Drops(); len(d) != 0 {
		t.Fatalf("drops must reset: %v", d)
	}
	if collect.DefaultMaxPerMetric != 5000 {
		t.Fatalf("spec bound is 5 000, got %d", collect.DefaultMaxPerMetric)
	}
}

// FR-009: ack removes only what was accepted; newer samples survive.
func TestBuffer_Ack(t *testing.T) {
	b := collect.NewBuffer(10)
	b.Add(dim("hostname", "a", 1))
	b.Add(dim("arch", "amd64", 1))
	for i := 1; i <= 4; i++ {
		b.Add(metric("cpu", float64(i), i))
	}
	b.Add(metric("mem", 9, 1))
	s := b.Snapshot()

	// Between snapshot and ack: a newer hostname and a newer cpu sample arrive.
	b.Add(dim("hostname", "b", 5))
	b.Add(metric("cpu", 5, 5))

	// Accept hostname (stale), arch, first 2 cpu samples, no mem.
	b.Ack(s, collect.Ack{Dims: []string{"hostname", "arch"}, Metrics: map[string]int{"cpu": 2}})
	s2 := b.Snapshot()
	if len(s2.Dims) != 1 || s2.Dims[0].Slug != "hostname" || s2.Dims[0].Value != "b" {
		t.Fatalf("dims after ack: %+v", s2.Dims)
	}
	vals := ""
	for _, m := range s2.Metrics {
		vals += fmt.Sprintf("%s=%v ", m.Slug, m.Value)
	}
	if vals != "cpu=3 cpu=4 cpu=5 mem=9 " {
		t.Fatalf("metrics after ack: %s", vals)
	}

	// Full ack empties the buffer.
	b.Ack(s2, collect.Ack{Dims: []string{"hostname"}, Metrics: map[string]int{"cpu": 3, "mem": 1}})
	if !b.Empty() {
		t.Fatalf("expected empty, got %+v", b.Snapshot())
	}
}

// FR-008 + FR-009: a drop between snapshot and ack must not shift the ack.
func TestBuffer_AckAfterDrop(t *testing.T) {
	b := collect.NewBuffer(3)
	b.Add(metric("cpu", 1, 1))
	b.Add(metric("cpu", 2, 2))
	b.Add(metric("cpu", 3, 3))
	s := b.Snapshot() // 1,2,3
	b.Add(metric("cpu", 4, 4))
	b.Add(metric("cpu", 5, 5)) // drops 1 and 2 → 3,4,5
	b.Ack(s, collect.Ack{Metrics: map[string]int{"cpu": 3}})
	s2 := b.Snapshot()
	if len(s2.Metrics) != 2 || s2.Metrics[0].Value != 4.0 || s2.Metrics[1].Value != 5.0 {
		t.Fatalf("metrics: %+v", s2.Metrics)
	}
	// Acking more than the snapshot held is clamped.
	b.Ack(s2, collect.Ack{Metrics: map[string]int{"cpu": 99}})
	if !b.Empty() {
		t.Fatal("expected empty")
	}
}
