package disk_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module/disk"
)

// steady returns a host whose first collection primes over two identical
// readings (base), and whose later collections read the given readings in
// order. That puts the arithmetic under test in collection 2, over whatever
// the test advances the clock by.
func steady(base []hostread.DiskCounters, later ...[]hostread.DiskCounters) *fakeReader {
	r := host(healthyVolume)
	r.counters = append([][]hostread.DiskCounters{base, base}, later...)
	return r
}

// primeThenAdvance runs the priming collection and moves the clock on by
// 30s, so that collection 2's rates are over 30 seconds.
func primeThenAdvance(t *testing.T, m *disk.Module, clk *fakeClock) {
	t.Helper()
	collectOK(t, m)
	clk.Advance(30 * time.Second)
}

// FR-012: throughput in MiB/s and operations per second, over the time since
// the previous reading, rounded to two decimal places.
func TestRates_MiBpsAndIops(t *testing.T) {
	r := steady(
		devs(dev("sda", hostread.KindDisk, 1000, 2000, 10, 20, 0)),
		devs(dev("sda", hostread.KindDisk, 1000+90*mib, 2000+45*mib, 10+300, 20+3001, 0)),
	)
	m, clk, buf := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	for key, want := range map[string]float64{
		disk.KeyReadMiBps:  3,      // 90 MiB / 30 s
		disk.KeyWriteMiBps: 1.5,    // 45 MiB / 30 s
		disk.KeyReadIOPS:   10,     // 300 / 30 s
		disk.KeyWriteIOPS:  100.03, // 3001 / 30 s = 100.0333…
	} {
		if v := value(t, got, key); v != want {
			t.Errorf("%s = %v, want %v", key, v, want)
		}
	}
	if omissionRecords(buf) != 0 {
		t.Fatalf("unexpected omissions:\n%s", buf)
	}
}

// FR-011, US-3/2: a write through LVM on a partition appears on the
// device-mapper device, the partition and the disk. It is counted once.
func TestRates_LVMOnPartition_CountedOnce(t *testing.T) {
	stack := func(written uint64) []hostread.DiskCounters {
		return devs(
			dev("dm-0", hostread.KindVirtual, 0, written, 0, 0, 0),
			dev("sda", hostread.KindDisk, 0, written, 0, 0, 0),
			dev("sda1", hostread.KindPartition, 0, written, 0, 0, 0),
		)
	}
	m, clk, _ := newTestModule(steady(stack(0), stack(gib)), "linux")
	primeThenAdvance(t, m, clk)
	if v := value(t, collectOK(t, m), disk.KeyWriteMiBps); v != 34.13 { // 1024 MiB / 30 s
		t.Fatalf("write = %v MiB/s, want 34.13 (1 GiB once, not three times)", v)
	}
}

// FR-011: Windows counts per lettered volume; volumes do not overlap, so every
// volume is counted.
func TestRates_WindowsVolumesSummed(t *testing.T) {
	r := steady(
		devs(dev("C:", hostread.KindVolume, 0, 0, 0, 0, 0), dev("D:", hostread.KindVolume, 0, 0, 0, 0, 0)),
		devs(dev("C:", hostread.KindVolume, 0, 30*mib, 0, 0, 0), dev("D:", hostread.KindVolume, 0, 60*mib, 0, 0, 0)),
	)
	r.windowsDir = `C:\Windows`
	m, clk, _ := newTestModule(r, "windows")
	primeThenAdvance(t, m, clk)
	if v := value(t, collectOK(t, m), disk.KeyWriteMiBps); v != 3 {
		t.Fatalf("write = %v MiB/s, want 3", v)
	}
}

// FR-013, US-3/3: busy is the busiest disk's share of wall time, not the
// average, clamped to 100.
func TestBusy_MaxNotAverage(t *testing.T) {
	cases := []struct {
		name         string
		sdaMs, sdbMs uint64
		want         float64
	}{
		{"one saturated, one idle", 30000, 0, 100},
		{"the busier one wins", 12345, 3000, 41.15},
		{"clamped above 100", 31000, 0, 100},
		{"both idle", 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := steady(
				devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 1000), dev("sdb", hostread.KindDisk, 0, 0, 0, 0, 1000)),
				devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 1000+c.sdaMs), dev("sdb", hostread.KindDisk, 0, 0, 0, 0, 1000+c.sdbMs)),
			)
			m, clk, _ := newTestModule(r, "linux")
			primeThenAdvance(t, m, clk)
			if v := value(t, collectOK(t, m), disk.KeyBusyPct); v != c.want {
				t.Fatalf("busy = %v, want %v", v, c.want)
			}
		})
	}
}

// FR-014, US-3/4: the first collection primes with a bounded 250ms pause and
// publishes a real rate from that window, so a one-shot run has one.
func TestIO_FirstCollectPrimes(t *testing.T) {
	r := host(healthyVolume)
	r.counters = [][]hostread.DiskCounters{
		devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0)),
		devs(dev("sda", hostread.KindDisk, 0, mib, 0, 25, 125)),
	}
	m, clk, _ := newTestModule(r, "linux")
	got := collectOK(t, m)
	if len(clk.sleeps) != 1 || clk.sleeps[0] != disk.DefaultPrime || disk.DefaultPrime != 250*time.Millisecond {
		t.Fatalf("want one 250ms prime, got %v", clk.sleeps)
	}
	if r.counterCalls() != 2 {
		t.Fatalf("want two readings in the first collect, got %d", r.counterCalls())
	}
	for key, want := range map[string]float64{
		disk.KeyWriteMiBps: 4,   // 1 MiB / 0.25 s
		disk.KeyWriteIOPS:  100, // 25 / 0.25 s
		disk.KeyBusyPct:    50,  // 125ms / 250ms
		disk.KeyReadMiBps:  0,
	} {
		if v := value(t, got, key); v != want {
			t.Errorf("%s = %v, want %v", key, v, want)
		}
	}
	// Later collections never pause (ADR-0006).
	clk.Advance(30 * time.Second)
	collectOK(t, m)
	if len(clk.sleeps) != 1 {
		t.Fatalf("only the first collection primes, got %v", clk.sleeps)
	}
}

// FR-014, FR-020: a deadline too short for the prime yields no I/O values and
// no omission record; that reading is the baseline for the next collection.
func TestIO_TightDeadlineSkipsPrime(t *testing.T) {
	r := steady(
		devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0)),
		devs(dev("sda", hostread.KindDisk, 0, 30*mib, 0, 0, 0)),
	)
	r.counters = r.counters[1:] // no duplicate: the first collect reads once
	m, clk, buf := newTestModule(r, "linux")
	ctx, cancel := context.WithDeadline(context.Background(), clk.Now().Add(10*time.Millisecond))
	defer cancel()
	obs, err := m.Collect(ctx)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := byKey(t, obs)
	absent(t, got, ioKeys...)
	value(t, got, disk.KeyRootUsedPct)
	if len(clk.sleeps) != 0 || omissionRecords(buf) != 0 {
		t.Fatalf("no pause and no omission expected: sleeps=%v\n%s", clk.sleeps, buf)
	}
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), disk.KeyWriteMiBps); v != 1 {
		t.Fatalf("second collect measures from the first reading: got %v, want 1", v)
	}
}

// FR-014: a pause cut short leaves the first reading as the baseline — never a
// mixture — and nothing is published for the window that was not measured.
func TestIO_CancelledPauseKeepsConsistentBaseline(t *testing.T) {
	r := host(healthyVolume)
	r.counters = [][]hostread.DiskCounters{
		devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0)),
		devs(dev("sda", hostread.KindDisk, 0, 60*mib, 0, 0, 0)),
	}
	m, clk, buf := newTestModule(r, "linux")
	clk.sleepErr = context.Canceled
	got := collectOK(t, m)
	absent(t, got, ioKeys...)
	if r.counterCalls() != 1 || omissionRecords(buf) != 0 {
		t.Fatalf("the cut-short collect reads once and reports nothing: calls=%d\n%s", r.counterCalls(), buf)
	}
	clk.sleepErr = nil
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), disk.KeyWriteMiBps); v != 2 {
		t.Fatalf("want 60 MiB over 30s from the kept baseline = 2, got %v", v)
	}
}

// FR-014: a device present in only one reading contributes nothing; its
// lifetime counters never appear as a one-interval spike.
func TestIO_HotplugDeviceContributesNothing(t *testing.T) {
	r := steady(
		devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0), dev("sdc", hostread.KindDisk, 0, 0, 0, 0, 0)),
		devs(
			dev("sda", hostread.KindDisk, 0, 30*mib, 0, 0, 3000),
			dev("sdb", hostread.KindDisk, 500*gib, 900*gib, 1e9, 1e9, 1e9), // plugged in
			// sdc removed
		),
	)
	m, clk, _ := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	if v := value(t, got, disk.KeyWriteMiBps); v != 1 {
		t.Fatalf("write = %v, want 1 (sda only)", v)
	}
	if v := value(t, got, disk.KeyBusyPct); v != 10 {
		t.Fatalf("busy = %v, want 10 (sda only)", v)
	}
	if v := value(t, got, disk.KeyReadMiBps); v != 0 {
		t.Fatalf("read = %v, want 0", v)
	}
}

// FR-014: a counter that went backwards (reset, replaced device, or Windows'
// 32-bit operation counts wrapping) drops only the values that depend on it,
// silently; the next collection measures from the new reading.
func TestIO_CounterRegression_DropsQuantity(t *testing.T) {
	const nearWrap = 1<<32 - 100
	r := steady(
		devs(dev("C:", hostread.KindVolume, 0, 0, 0, nearWrap, 0)),
		devs(dev("C:", hostread.KindVolume, 30*mib, 30*mib, 30, 200, 0)), // write ops wrapped
		devs(dev("C:", hostread.KindVolume, 30*mib, 30*mib, 30, 200+300, 0)),
	)
	r.windowsDir = `C:\Windows`
	m, clk, buf := newTestModule(r, "windows")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	absent(t, got, disk.KeyWriteIOPS)
	for _, k := range []string{disk.KeyReadMiBps, disk.KeyWriteMiBps, disk.KeyReadIOPS} {
		if v := value(t, got, k); v != 1 {
			t.Errorf("%s = %v, want 1", k, v)
		}
	}
	if omissionRecords(buf) != 0 {
		t.Fatalf("a regression is not an omission:\n%s", buf)
	}
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), disk.KeyWriteIOPS); v != 10 {
		t.Fatalf("after the wrap: write iops = %v, want 10", v)
	}
}

// FR-014: two readings with no time between them produce no I/O values and
// no division error.
func TestIO_ZeroElapsed(t *testing.T) {
	m, _, buf := newTestModule(steady(devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0))), "linux")
	collectOK(t, m)
	got := collectOK(t, m) // the clock has not moved
	absent(t, got, ioKeys...)
	value(t, got, disk.KeyRootUsedPct)
	if omissionRecords(buf) != 0 {
		t.Fatalf("zero elapsed is not an omission:\n%s", buf)
	}
}

// FR-015: with no device to count, the I/O values are omitted and reported —
// never published as zeros, because "no data" is not "idle".
func TestIO_NoCountedDevices_OmittedNotZero(t *testing.T) {
	cases := map[string][]hostread.DiskCounters{
		"nothing reported (counters disabled)": nil,
		"only partitions and stacked devices": devs(
			dev("sda1", hostread.KindPartition, 0, 0, 0, 0, 0),
			dev("dm-0", hostread.KindVirtual, 0, 0, 0, 0, 0),
			dev("loop0", hostread.KindVirtual, 0, 0, 0, 0, 0),
		),
	}
	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			m, clk, buf := newTestModule(steady(cs), "linux")
			for range 2 {
				got := collectOK(t, m) // the space values keep the collection alive
				absent(t, got, ioKeys...)
				clk.Advance(30 * time.Second)
			}
			if omissionRecords(buf) != 2 || !strings.Contains(buf.String(), disk.KeyWriteMiBps) {
				t.Fatalf("want one omission record per collection naming the I/O keys:\n%s", buf)
			}
		})
	}
}

// FR-017, FR-020: a failed counter reading costs only the I/O values, is
// reported, and leaves the baseline untouched for the next collection.
func TestIO_ReadError(t *testing.T) {
	r := steady(
		devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0)),
		devs(dev("sda", hostread.KindDisk, 0, 60*mib, 0, 0, 0)),
	)
	m, clk, buf := newTestModule(r, "linux")
	primeThenAdvance(t, m, clk)
	r.countersErr = errRead
	got := collectOK(t, m)
	absent(t, got, ioKeys...)
	value(t, got, disk.KeyRootTotal)
	if omissionRecords(buf) != 1 || !strings.Contains(buf.String(), errRead.Error()) {
		t.Fatalf("want one omission record with the cause:\n%s", buf)
	}
	r.countersErr = nil
	clk.Advance(30 * time.Second)
	if v := value(t, collectOK(t, m), disk.KeyWriteMiBps); v != 1 {
		t.Fatalf("measured from the untouched baseline over 60s: got %v, want 1", v)
	}
}
