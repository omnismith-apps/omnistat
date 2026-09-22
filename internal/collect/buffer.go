package collect

import (
	"sort"
	"sync"
	"time"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// DefaultMaxPerMetric is the buffer bound per metric attribute (FR-008).
const DefaultMaxPerMetric = 5000

// Sample is one validated, stamped observation bound to its target attribute.
type Sample struct {
	Module string
	Key    string
	Slug   string
	Kind   manifest.Kind
	// Value has the canonical type for Kind (see Validate).
	Value any
	// At is when the provider returned it (FR-006), UTC.
	At time.Time
}

// IsMetric reports whether the sample belongs to a metric attribute.
func (s Sample) IsMetric() bool { return s.Kind == manifest.KindMetric }

// Batch is a snapshot of the buffer: what one publish will send. Dims are
// sorted by slug; Metrics are sorted by slug, then collection order.
type Batch struct {
	Dims    []Sample
	Metrics []Sample
	// start is the absolute index of each slug's first metric sample.
	start map[string]uint64
}

// Empty reports whether there is nothing to publish.
func (b Batch) Empty() bool { return len(b.Dims) == 0 && len(b.Metrics) == 0 }

// Ack says what the platform accepted from a Batch: dimension slugs, and
// per metric slug how many samples counting from the batch's first.
type Ack struct {
	Dims    []string
	Metrics map[string]int
}

// Buffer holds samples between publishes (FR-007…009): the latest sample
// per dimension, every sample per metric up to a bound. Safe for
// concurrent use.
type Buffer struct {
	mu      sync.Mutex
	max     int
	dims    map[string]Sample
	metrics map[string]*queue
	drops   map[string]int
}

// queue is one metric's pending samples plus how many were ever removed
// from its front, so that indexes stay absolute across drops and acks.
type queue struct {
	items   []Sample
	removed uint64
}

// NewBuffer returns a buffer bounded at maxPerMetric samples per metric
// (≤ 0 means DefaultMaxPerMetric).
func NewBuffer(maxPerMetric int) *Buffer {
	if maxPerMetric <= 0 {
		maxPerMetric = DefaultMaxPerMetric
	}
	return &Buffer{max: maxPerMetric, dims: map[string]Sample{}, metrics: map[string]*queue{}, drops: map[string]int{}}
}

// Add stores a sample: latest wins for dimensions (FR-007); metrics append,
// dropping the oldest when the bound is reached (FR-008).
func (b *Buffer) Add(s Sample) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.IsMetric() {
		b.dims[s.Slug] = s
		return
	}
	q := b.metrics[s.Slug]
	if q == nil {
		q = &queue{}
		b.metrics[s.Slug] = q
	}
	if len(q.items) >= b.max {
		q.items = q.items[1:]
		q.removed++
		b.drops[s.Slug]++
	}
	q.items = append(q.items, s)
}

// Empty reports whether nothing is pending.
func (b *Buffer) Empty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.dims) > 0 {
		return false
	}
	for _, q := range b.metrics {
		if len(q.items) > 0 {
			return false
		}
	}
	return true
}

// Snapshot copies what is pending into a Batch. The buffer keeps everything
// until Ack (FR-009).
func (b *Buffer) Snapshot() Batch {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := Batch{start: map[string]uint64{}}
	for _, s := range b.dims {
		out.Dims = append(out.Dims, s)
	}
	sort.Slice(out.Dims, func(i, j int) bool { return out.Dims[i].Slug < out.Dims[j].Slug })
	slugs := make([]string, 0, len(b.metrics))
	for slug, q := range b.metrics {
		if len(q.items) > 0 {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		q := b.metrics[slug]
		out.start[slug] = q.removed
		out.Metrics = append(out.Metrics, q.items...)
	}
	return out
}

// Ack removes accepted samples: a dimension only if the buffer still holds
// the snapshotted sample (a newer one stays), a metric's first n samples
// counted from the snapshot's start (already-dropped ones are not
// double-counted).
func (b *Buffer) Ack(batch Batch, a Ack) {
	b.mu.Lock()
	defer b.mu.Unlock()
	snap := make(map[string]time.Time, len(batch.Dims))
	for _, s := range batch.Dims {
		snap[s.Slug] = s.At
	}
	for _, slug := range a.Dims {
		if cur, ok := b.dims[slug]; ok && cur.At.Equal(snap[slug]) {
			delete(b.dims, slug)
		}
	}
	for slug, n := range a.Metrics {
		q := b.metrics[slug]
		start, ok := batch.start[slug]
		if q == nil || !ok || n <= 0 {
			continue
		}
		end := start + uint64(n)
		if end <= q.removed {
			continue
		}
		drop := int(end - q.removed) //nolint:gosec // bounded by len(q.items) below
		if drop > len(q.items) {
			drop = len(q.items)
		}
		q.items = q.items[drop:]
		q.removed += uint64(drop)
	}
}

// Drops returns how many samples were dropped per metric slug since the
// last call and resets the counters (FR-008: logged once per interval).
func (b *Buffer) Drops() map[string]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.drops
	b.drops = map[string]int{}
	return out
}
