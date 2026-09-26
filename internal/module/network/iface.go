package network

import (
	"context"
	"sort"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// reading is one reading of the rate areas and the instant it was taken. An
// area whose read failed is nil, so the next collection has no baseline for
// it rather than a stale one (FR-015).
type reading struct {
	at time.Time
	// ifaces holds the physical interfaces only, by ID (FR-005).
	ifaces map[string]hostread.IfaceCounters
	stack  *hostread.StackCounters
	// listen is the cumulative listen-drop count; nil off Linux too.
	listen *uint64
}

// readErrs are the areas that could not be read, for the omission record.
type readErrs struct {
	ifaces, stack, listen error
}

// names lists the counted interfaces' names, sorted, for the debug record.
func (r reading) names() []string {
	out := make([]string, 0, len(r.ifaces))
	for _, c := range r.ifaces {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

// read takes one reading of each rate area, independently (FR-017, FR-019).
func (m *Module) read(ctx context.Context, linux bool) (reading, readErrs) {
	var r reading
	var e readErrs
	if cs, err := m.Reader.Interfaces(ctx); err != nil {
		e.ifaces = err
	} else {
		r.ifaces = make(map[string]hostread.IfaceCounters, len(cs))
		for _, c := range cs {
			if c.Physical {
				r.ifaces[c.ID] = c
			}
		}
	}
	if s, err := m.Reader.Stack(ctx); err != nil {
		e.stack = err
	} else {
		r.stack = &s
	}
	if linux {
		if n, err := m.Reader.ListenDrops(ctx); err != nil {
			e.listen = err
		} else {
			r.listen = &n
		}
	}
	r.at = m.now()
	return r, e
}

// rates returns the rate observations and the reading they end at (spec 010
// FR-005…FR-015, ADR-0006).
//
// The retained reading is assigned exactly once, so a cancelled or failed
// call leaves either the new reading or the old one as the baseline, never a
// mixture (FR-015). Omissions come from the reading that is kept: an area
// that failed only in the first of the priming pair simply has no baseline.
func (m *Module) rates(ctx context.Context, linux bool, om *module.Omissions) ([]module.Observation, reading) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, errs := m.read(ctx, linux)
	prev := m.last
	if prev == nil && m.canPrime(ctx) && m.Sleep(ctx, m.prime()) == nil {
		// First collection: a second reading after a bounded pause, so that
		// a one-shot run publishes real rates. Values withheld because the
		// deadline was too short are not omissions (FR-022).
		first := cur
		cur, errs = m.read(ctx, linux)
		prev = &first
	}
	m.last = &cur

	addAll(om, errs.ifaces, ifaceKeys...)
	addAll(om, errs.stack, stackKeys...)
	addAll(om, errs.listen, KeyTCPListenDropsPs)
	if cur.ifaces != nil && len(cur.ifaces) == 0 {
		m.noIface.Do(func() {
			m.logger().Info("no physical network interface; interface values are not published", "module", Name)
		})
	}

	if prev == nil {
		return nil, cur
	}
	elapsed := cur.at.Sub(prev.at)
	if elapsed <= 0 {
		return nil, cur
	}
	secs := elapsed.Seconds()
	obs := ifaceRates(*prev, cur, secs)
	return append(obs, stackRates(*prev, cur, secs)...), cur
}

// ifaceKeys are the values the interface reading produces.
var ifaceKeys = []string{KeyRxMbps, KeyTxMbps, KeyRxPps, KeyTxPps, KeyRxErrorsPs, KeyTxErrorsPs, KeyRxDropsPs, KeyTxDropsPs}

// ifaceRates computes the interface values between two readings (FR-006…
// FR-009, FR-015).
//
// Only interfaces present in both readings count, so a hot-plugged or renamed
// interface's lifetime counters never appear as a spike, and with none in
// common there is nothing to say. A counter that went backwards on any of
// them drops the value depending on it.
func ifaceRates(prev, cur reading, secs float64) []module.Observation {
	if prev.ifaces == nil || cur.ifaces == nil {
		return nil
	}
	var common []string
	for id := range cur.ifaces {
		if _, ok := prev.ifaces[id]; ok {
			common = append(common, id)
		}
	}
	if len(common) == 0 {
		return nil
	}
	delta := func(f func(hostread.IfaceCounters) uint64) (sum uint64, ok bool) {
		for _, id := range common {
			p, c := f(prev.ifaces[id]), f(cur.ifaces[id])
			if c < p {
				return 0, false
			}
			sum += c - p
		}
		return sum, true
	}
	// Throughput is bytes × 8 in decimal megabits (FR-006); the rest are
	// counts per second (FR-007, FR-008).
	var obs []module.Observation
	for _, q := range []struct {
		key      string
		f        func(hostread.IfaceCounters) uint64
		mul, div float64
	}{
		{KeyRxMbps, func(c hostread.IfaceCounters) uint64 { return c.RxBytes }, 8, 1e6},
		{KeyTxMbps, func(c hostread.IfaceCounters) uint64 { return c.TxBytes }, 8, 1e6},
		{KeyRxPps, func(c hostread.IfaceCounters) uint64 { return c.RxPackets }, 1, 1},
		{KeyTxPps, func(c hostread.IfaceCounters) uint64 { return c.TxPackets }, 1, 1},
		{KeyRxErrorsPs, func(c hostread.IfaceCounters) uint64 { return c.RxErrors }, 1, 1},
		{KeyTxErrorsPs, func(c hostread.IfaceCounters) uint64 { return c.TxErrors }, 1, 1},
		{KeyRxDropsPs, func(c hostread.IfaceCounters) uint64 { return c.RxDrops }, 1, 1},
		{KeyTxDropsPs, func(c hostread.IfaceCounters) uint64 { return c.TxDrops }, 1, 1},
	} {
		if d, ok := delta(q.f); ok {
			obs = append(obs, module.Observation{Key: q.key, Value: round2(float64(d) * q.mul / q.div / secs)})
		}
	}
	return obs
}

// canPrime reports whether the call has time to spare for the prime pause
// (FR-015).
func (m *Module) canPrime(ctx context.Context) bool {
	dl, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return dl.Sub(m.now()) >= m.prime()+primeSlack
}

func (m *Module) prime() time.Duration {
	if m.Prime > 0 {
		return m.Prime
	}
	return DefaultPrime
}
