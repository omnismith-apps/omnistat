package collect

import (
	"log/slog"
	"sort"
	"sync"
)

// MaxKeysPerModule bounds the module-owned entities one module may feed in a
// process (spec 011 FR-009).
const MaxKeysPerModule = 256

// Buffers is the host's Buffer plus one Buffer per module-owned entity
// (spec 011 FR-016): each entity's samples are bounded, acknowledged and kept
// on failure on their own. Safe for concurrent use.
type Buffers struct {
	max int
	log *slog.Logger

	mu      sync.Mutex
	host    *Buffer
	targets map[Target]*Buffer
	keys    map[string]int  // module → targets created
	warned  map[string]bool // module → cap warning logged
}

// NewBuffers returns an empty set bounded at maxPerMetric samples per metric
// attribute and entity (≤ 0 means DefaultMaxPerMetric).
func NewBuffers(maxPerMetric int, log *slog.Logger) *Buffers {
	if log == nil {
		log = slog.Default()
	}
	return &Buffers{max: maxPerMetric, log: log, host: NewBuffer(maxPerMetric),
		targets: map[Target]*Buffer{}, keys: map[string]int{}, warned: map[string]bool{}}
}

// Add routes a sample to its entity's buffer. A sample for a new key of a
// module that already feeds MaxKeysPerModule entities is dropped, with one
// warning per module per process (FR-009).
func (b *Buffers) Add(s Sample) {
	if s.Target.IsHost() {
		b.host.Add(s)
		return
	}
	b.mu.Lock()
	buf, ok := b.targets[s.Target]
	if !ok {
		if b.keys[s.Target.Module] >= MaxKeysPerModule {
			warn := !b.warned[s.Target.Module]
			b.warned[s.Target.Module] = true
			b.mu.Unlock()
			if warn {
				b.log.Warn("too many entities for one module; observations for new keys dropped",
					"module", s.Target.Module, "max", MaxKeysPerModule, "key", s.Target.Key)
			}
			return
		}
		buf = NewBuffer(b.max)
		b.targets[s.Target] = buf
		b.keys[s.Target.Module]++
	}
	b.mu.Unlock()
	buf.Add(s)
}

// Host is the host entity's buffer.
func (b *Buffers) Host() *Buffer { return b.host }

// Targets lists the module-owned entities that have a buffer, ordered by
// module, template and key (FR-017).
func (b *Buffers) Targets() []Target {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Target, 0, len(b.targets))
	for t := range b.targets {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.Module != c.Module {
			return a.Module < c.Module
		}
		if a.Template != c.Template {
			return a.Template < c.Template
		}
		return a.Key < c.Key
	})
	return out
}

// For is the buffer of one module-owned entity, nil if it has none.
func (b *Buffers) For(t Target) *Buffer {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.targets[t]
}

// Empty reports whether nothing is pending for any entity.
func (b *Buffers) Empty() bool {
	if !b.host.Empty() {
		return false
	}
	for _, t := range b.Targets() {
		if !b.For(t).Empty() {
			return false
		}
	}
	return true
}

// Drop is a count of metric samples dropped from one entity's full buffer.
type Drop struct {
	Target  Target
	Slug    string
	Dropped int
}

// Drops returns the samples dropped since the last call, for every entity,
// and resets the counters (003 FR-008).
func (b *Buffers) Drops() []Drop {
	var out []Drop
	add := func(t Target, m map[string]int) {
		slugs := make([]string, 0, len(m))
		for slug := range m {
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs)
		for _, slug := range slugs {
			out = append(out, Drop{Target: t, Slug: slug, Dropped: m[slug]})
		}
	}
	add(Target{}, b.host.Drops())
	for _, t := range b.Targets() {
		add(t, b.For(t).Drops())
	}
	return out
}
