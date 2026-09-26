package disk_test

import (
	"context"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module/disk"
)

// host is a fake host with the given system volume and one idle disk, so that
// the space tests see no I/O omission.
func host(u hostread.VolumeUsage) *fakeReader {
	return &fakeReader{
		usage:    u,
		counters: [][]hostread.DiskCounters{devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0))},
	}
}

// FR-006: the system volume is the filesystem the OS lives on, per platform.
func TestSystemVolume(t *testing.T) {
	cases := []struct {
		name, goos string
		r          *fakeReader
		want       string
	}{
		{"linux is /", "linux", host(healthyVolume), "/"},
		{
			// From macOS 10.15, / is a sealed snapshot; the data volume fills.
			"darwin reads the data volume", "darwin",
			func() *fakeReader {
				r := host(healthyVolume)
				r.dirs = map[string]bool{"/System/Volumes/Data": true}
				return r
			}(),
			"/System/Volumes/Data",
		},
		{"darwin without a data volume falls back to /", "darwin", host(healthyVolume), "/"},
		{
			// Not assumed to be C:.
			"windows reads the Windows directory's drive", "windows",
			func() *fakeReader { r := host(healthyVolume); r.windowsDir = `D:\Windows`; return r }(),
			`D:\`,
		},
		{
			"windows drive letter is normalised", "windows",
			func() *fakeReader { r := host(healthyVolume); r.windowsDir = `c:\windows`; return r }(),
			`C:\`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _, _ := newTestModule(c.r, c.goos)
			collectOK(t, m)
			if len(c.r.usagePaths) != 1 || c.r.usagePaths[0] != c.want {
				t.Fatalf("read %v, want exactly [%s]", c.r.usagePaths, c.want)
			}
		})
	}
}

// FR-006, FR-017: a Windows directory with no drive letter cannot name the
// system volume. The space values are omitted and reported; I/O is kept.
func TestSystemVolume_WindowsNoDrive(t *testing.T) {
	for _, dir := range []string{`\\?\Volume{0b1c}\Windows`, "", "Windows"} {
		r := host(healthyVolume)
		r.windowsDir = dir
		m, _, buf := newTestModule(r, "windows")
		got := collectOK(t, m)
		absent(t, got, disk.KeyRootUsedPct, disk.KeyRootAvailable, disk.KeyRootTotal)
		value(t, got, disk.KeyReadMiBps)
		if len(r.usagePaths) != 0 {
			t.Errorf("%q: nothing must be read, read %v", dir, r.usagePaths)
		}
		if omissionRecords(buf) != 1 || !strings.Contains(buf.String(), "no drive letter") {
			t.Errorf("%q: want one omission record naming the cause:\n%s", dir, buf)
		}
	}
}

// FR-007: used ÷ (used + available), as df computes it. The superuser reserve
// counts neither as used nor as available (US-1/2).
func TestRootUsedPct_DfFormula(t *testing.T) {
	// 1000 GiB, 600 used, 350 available: 50 GiB reserved.
	m, _, _ := newTestModule(host(vol(1000*gib, 600*gib, 350*gib, 0, 0)), "linux")
	got := collectOK(t, m)
	if v := value(t, got, disk.KeyRootUsedPct); v != 63.16 { // 600/950 = 63.157…
		t.Fatalf("used pct = %v, want 63.16 (df), not 60 (used/total)", v)
	}
	if v := value(t, got, disk.KeyRootAvailable); v != 350 {
		t.Fatalf("available = %v, want 350", v)
	}
	if v := value(t, got, disk.KeyRootTotal); v != 1000 {
		t.Fatalf("total = %v, want 1000", v)
	}
}

// FR-007: the percentage is clamped to [0, 100] and published with two
// decimal places, half away from zero, never as float noise.
func TestRootUsedPct_RoundedAndClamped(t *testing.T) {
	cases := []struct {
		used, avail uint64
		want        float64
	}{
		{0, 10 * gib, 0},
		{10 * gib, 0, 100}, // full to the reserve: df shows 100%
		{1, 2, 33.33},
		{2, 1, 66.67},
		{1, 7, 12.5},
		{1, 7999, 0.01}, // 0.0125 → 0.01
		{3, 7997, 0.04}, // 0.0375 → 0.04
	}
	for _, c := range cases {
		m, _, _ := newTestModule(host(vol(100*gib, c.used, c.avail, 0, 0)), "linux")
		if v := value(t, collectOK(t, m), disk.KeyRootUsedPct); v != c.want {
			t.Errorf("used=%d avail=%d: got %v, want %v", c.used, c.avail, v, c.want)
		}
	}
}

// FR-008: amounts are GiB with two decimal places, rounded DOWN — headroom is
// never overstated, and a volume one byte short of 1 GiB is 0.99, not 1.
func TestFloorGiB2(t *testing.T) {
	cases := []struct {
		bytes uint64
		want  float64
	}{
		{gib - 1, 0.99},
		{gib, 1},
		{gib + gib/2, 1.5},
		{gib + 7*gib/100 + 1, 1.07},
		{5 * mib, 0}, // 0.0048…
		{11 * mib, 0.01},
		{8*gib - 1, 7.99},
		{16 << 40, 16384},            // 16 TiB
		{(160 << 40) - 1, 163839.99}, // ~160 TiB: still two exact decimals
	}
	for _, c := range cases {
		m, _, _ := newTestModule(host(vol(c.bytes, 0, c.bytes, 0, 0)), "linux")
		got := collectOK(t, m)
		if v := value(t, got, disk.KeyRootTotal); v != c.want {
			t.Errorf("total %d bytes: got %v, want %v", c.bytes, v, c.want)
		}
		if v := value(t, got, disk.KeyRootAvailable); v != c.want {
			t.Errorf("available %d bytes: got %v, want %v", c.bytes, v, c.want)
		}
	}
}

// FR-010: a volume that reports no size yields no space values and is
// reported; it is not published as 0% of nothing.
func TestSpace_EmptyVolume_Omitted(t *testing.T) {
	for _, u := range []hostread.VolumeUsage{
		vol(0, 0, 0, 0, 0),
		vol(10*gib, 0, 0, 0, 0), // used + available = 0
	} {
		m, _, buf := newTestModule(host(u), "linux")
		got := collectOK(t, m) // I/O keeps the collection alive (FR-017)
		absent(t, got, disk.KeyRootUsedPct, disk.KeyRootAvailable, disk.KeyRootTotal)
		if omissionRecords(buf) != 1 || !strings.Contains(buf.String(), disk.KeyRootTotal) {
			t.Errorf("%+v: want one omission record naming the space keys:\n%s", u, buf)
		}
	}
}

// FR-005, FR-010: one reading per collection, and the size is observed on
// every collection, not only the first (US-2/2: a grown volume shows up).
func TestSpace_OneReadingAndTotalEveryCollect(t *testing.T) {
	r := host(healthyVolume)
	m, clk, _ := newTestModule(r, "linux")
	for i := range 3 {
		if i == 2 {
			r.usage = vol(200*gib, 40*gib, 160*gib, 1000, 400)
		}
		got := collectOK(t, m)
		want := 100.0
		if i == 2 {
			want = 200
		}
		if v := value(t, got, disk.KeyRootTotal); v != want {
			t.Fatalf("collect %d: total %v, want %v", i, v, want)
		}
		clk.Advance(disk.DefaultInterval)
	}
	if len(r.usagePaths) != 3 {
		t.Fatalf("want one volume reading per collect, got %d", len(r.usagePaths))
	}
}

// FR-009: (total − free) ÷ total, clamped and rounded like FR-007.
func TestInodes_Percent(t *testing.T) {
	m, _, _ := newTestModule(host(vol(100*gib, 40*gib, 60*gib, 3000, 1000)), "linux")
	if v := value(t, collectOK(t, m), disk.KeyRootInodesUsedPct); v != 66.67 {
		t.Fatalf("inode pct = %v, want 66.67", v)
	}
}

// FR-009, FR-020: more free inodes than inodes is an inconsistent reading;
// it costs only the inode value, which is reported.
func TestInodes_FreeExceedsTotal_Omitted(t *testing.T) {
	m, _, buf := newTestModule(host(vol(100*gib, 40*gib, 60*gib, 100, 200)), "linux")
	got := collectOK(t, m)
	absent(t, got, disk.KeyRootInodesUsedPct)
	value(t, got, disk.KeyRootUsedPct)
	if omissionRecords(buf) != 1 || !strings.Contains(buf.String(), disk.KeyRootInodesUsedPct) {
		t.Fatalf("want one omission record naming the inode key:\n%s", buf)
	}
}

// FR-009, FR-019, US-4/2: btrfs reports no inode limit. That is logged once
// per process at info, never as an omission, and the collection is complete.
func TestInodes_NoLimit_LoggedOnceNotOmitted(t *testing.T) {
	m, clk, buf := newTestModule(host(vol(100*gib, 40*gib, 60*gib, 0, 0)), "linux")
	for range 3 {
		got := collectOK(t, m)
		absent(t, got, disk.KeyRootInodesUsedPct)
		value(t, got, disk.KeyRootUsedPct)
		clk.Advance(disk.DefaultInterval)
	}
	if n := strings.Count(buf.String(), "no inode limit"); n != 1 {
		t.Fatalf("want exactly one notice, got %d:\n%s", n, buf)
	}
	if !strings.Contains(buf.String(), "level=INFO msg=\"no inode limit") {
		t.Fatalf("the notice must be at info level:\n%s", buf)
	}
	if omissionRecords(buf) != 0 {
		t.Fatalf("no inode limit is not an omission:\n%s", buf)
	}
}

// FR-017: a failed volume reading costs the space values (and on Linux the
// inode value) only; it is reported once, and I/O is still published.
func TestSpace_ReadError_IOStillPublished(t *testing.T) {
	r := host(healthyVolume)
	r.usageErr = errRead
	m, _, buf := newTestModule(r, "linux")
	got, err := m.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	obs := byKey(t, got)
	absent(t, obs, disk.KeyRootUsedPct, disk.KeyRootAvailable, disk.KeyRootTotal, disk.KeyRootInodesUsedPct)
	value(t, obs, disk.KeyWriteMiBps)
	if omissionRecords(buf) != 1 || !strings.Contains(buf.String(), disk.KeyRootInodesUsedPct) {
		t.Fatalf("want one record naming the space and inode keys:\n%s", buf)
	}
}
