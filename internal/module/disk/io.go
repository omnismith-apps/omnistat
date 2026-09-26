package disk

import (
	"context"
	"sort"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// ioReading is one reading of the counted devices' cumulative counters and
// the instant it was taken.
type ioReading struct {
	at  time.Time
	dev map[string]hostread.DiskCounters
}

func (r ioReading) names() []string {
	out := make([]string, 0, len(r.dev))
	for n := range r.dev {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// counted reports whether a device's I/O is counted (FR-011): whole physical
// disks and Windows volumes. Partitions and stacked or memory devices are not,
// because their I/O is counted on the disks beneath them or never reaches one.
func counted(k hostread.DeviceKind) bool {
	return k == hostread.KindDisk || k == hostread.KindVolume
}

// read takes one counter reading, keeping the counted devices only.
func (m *Module) read(ctx context.Context) (ioReading, error) {
	cs, err := m.Reader.Counters(ctx)
	if err != nil {
		return ioReading{}, err
	}
	r := ioReading{at: m.now(), dev: make(map[string]hostread.DiskCounters, len(cs))}
	for _, c := range cs {
		if counted(c.Kind) {
			r.dev[c.Name] = c
		}
	}
	return r, nil
}

// io returns the I/O observations, the names of the devices counted, and the
// error that prevented a reading (spec 008 FR-011…FR-015, ADR-0006).
//
// The retained reading is assigned exactly once on every path that read, so a
// cancelled or failed call leaves either the new reading or the old one as the
// baseline, never a mixture; a failed read leaves it untouched (FR-014).
func (m *Module) io(ctx context.Context, withBusy bool) ([]module.Observation, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, err := m.read(ctx)
	if err != nil {
		return nil, nil, err
	}
	if m.last != nil {
		prev := *m.last
		m.last = &cur
		obs, err := rates(prev, cur, withBusy)
		return obs, cur.names(), err
	}

	// First collection: prime with a second reading after a bounded pause,
	// so that a one-shot run publishes real rates (FR-014). Values withheld
	// here are not omissions (FR-020).
	if !m.canPrime(ctx) {
		m.last = &cur
		return nil, cur.names(), nil
	}
	if err := m.Sleep(ctx, m.prime()); err != nil {
		m.last = &cur
		return nil, cur.names(), nil
	}
	next, err := m.read(ctx)
	if err != nil {
		m.last = &cur
		return nil, cur.names(), nil
	}
	m.last = &next
	obs, err := rates(cur, next, withBusy)
	return obs, next.names(), err
}

// rates computes the I/O values between two readings (FR-012…FR-015).
//
// Only devices present in both readings count, so a hot-plugged disk's
// lifetime counters never appear as a spike. A counter that went backwards on
// any of them drops the values depending on it — silently, as cpu does
// (004 FR-013) — and no elapsed time yields nothing. No device at all is an
// error: "no data" is not "idle".
func rates(prev, cur ioReading, withBusy bool) ([]module.Observation, error) {
	var common []string
	for name := range cur.dev {
		if _, ok := prev.dev[name]; ok {
			common = append(common, name)
		}
	}
	if len(common) == 0 {
		return nil, errNoDevices
	}
	elapsed := cur.at.Sub(prev.at)
	if elapsed <= 0 {
		return nil, nil
	}
	secs := elapsed.Seconds()

	// delta sums one counter's increase over the common devices; ok is false
	// if any of them went backwards.
	delta := func(f func(hostread.DiskCounters) uint64) (sum uint64, ok bool) {
		for _, n := range common {
			p, c := f(prev.dev[n]), f(cur.dev[n])
			if c < p {
				return 0, false
			}
			sum += c - p
		}
		return sum, true
	}

	var obs []module.Observation
	for _, q := range []struct {
		key   string
		f     func(hostread.DiskCounters) uint64
		scale float64
	}{
		{KeyReadMiBps, func(c hostread.DiskCounters) uint64 { return c.ReadBytes }, 1 << 20},
		{KeyWriteMiBps, func(c hostread.DiskCounters) uint64 { return c.WriteBytes }, 1 << 20},
		{KeyReadIOPS, func(c hostread.DiskCounters) uint64 { return c.ReadOps }, 1},
		{KeyWriteIOPS, func(c hostread.DiskCounters) uint64 { return c.WriteOps }, 1},
	} {
		if d, ok := delta(q.f); ok {
			obs = append(obs, module.Observation{Key: q.key, Value: round2(float64(d) / q.scale / secs)})
		}
	}

	if withBusy {
		// FR-013: the busiest device, not the average, which would hide one
		// saturated disk behind idle ones.
		var busiest uint64
		ok := true
		for _, n := range common {
			p, c := prev.dev[n].BusyMillis, cur.dev[n].BusyMillis
			if c < p {
				ok = false
				break
			}
			busiest = max(busiest, c-p)
		}
		if ok {
			obs = append(obs, module.Observation{Key: KeyBusyPct, Value: pct(float64(busiest), float64(elapsed.Milliseconds()))})
		}
	}
	return obs, nil
}

// canPrime reports whether the call has time to spare for the prime pause
// (FR-014).
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
