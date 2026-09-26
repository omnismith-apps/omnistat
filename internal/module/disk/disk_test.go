package disk_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/disk"
)

// FR-001, FR-003, FR-016: the manifest is the module's contract with the
// project schema, pinned attribute by attribute. Changing a default slug is a
// breaking change that needs an ADR (001 FR-002).
func TestManifest_DefaultSlugs(t *testing.T) {
	m := disk.New().Manifest()
	if m.Module != "disk" {
		t.Fatalf("module name: %q", m.Module)
	}
	if len(m.Templates) != 0 {
		t.Fatalf("disk attaches to the host template, declaring none of its own: %+v", m.Templates)
	}
	want := []struct {
		key, slug, name string
		kind            manifest.Kind
		platforms       string
	}{
		{"root_used_pct", "disk_root_used_pct", "System volume used", manifest.KindMetric, ""},
		{"root_available", "disk_root_available_gib", "System volume available", manifest.KindMetric, ""},
		{"root_total", "disk_root_total_gib", "System volume size", manifest.KindNumber, ""},
		{"root_inodes_used_pct", "disk_root_inodes_used_pct", "System volume inodes used", manifest.KindMetric, "linux"},
		{"read_mibps", "disk_read_mibps", "Disk read throughput", manifest.KindMetric, ""},
		{"write_mibps", "disk_write_mibps", "Disk write throughput", manifest.KindMetric, ""},
		{"read_iops", "disk_read_iops", "Disk read operations", manifest.KindMetric, ""},
		{"write_iops", "disk_write_iops", "Disk write operations", manifest.KindMetric, ""},
		{"busy_pct", "disk_busy_pct", "Busiest disk utilisation", manifest.KindMetric, "linux"},
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
		if len(a.Options) != 0 || a.Template != "" {
			t.Errorf("%s: no options and the host template expected: %+v", w.key, a)
		}
		if strings.TrimSpace(a.Description) == "" {
			t.Errorf("%s: description is empty", w.key)
		}
	}
	if err := manifest.Validate([]manifest.Manifest{m}); err != nil {
		t.Fatalf("manifest must validate: %v", err)
	}
}

// FR-004: the default cadence is 30s.
func TestDefaultInterval(t *testing.T) {
	m := disk.New()
	if got := m.DefaultInterval(); got != 30*time.Second {
		t.Fatalf("default interval: %v", got)
	}
	if _, ok := module.ProviderOf(m); !ok {
		t.Fatal("disk must be a provider")
	}
	if m.Name() != disk.Name {
		t.Fatalf("Name() = %q", m.Name())
	}
}

// NFR-006: the real module reads the host through hostread.
func TestNew_UsesHost(t *testing.T) {
	if _, ok := disk.New().Reader.(hostread.Disk); !ok {
		t.Fatalf("New must read through hostread.Disk, got %T", disk.New().Reader)
	}
}

// FR-011…FR-013 on Linux: every attribute is published from a healthy host.
func TestCollect_Linux_AllNine(t *testing.T) {
	m, clk, buf := newTestModule(steady(devs(dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0))), "linux")
	primeThenAdvance(t, m, clk)
	got := collectOK(t, m)
	for _, k := range append([]string{disk.KeyRootUsedPct, disk.KeyRootAvailable, disk.KeyRootTotal, disk.KeyRootInodesUsedPct}, ioKeys...) {
		value(t, got, k)
	}
	if len(got) != 9 || omissionRecords(buf) != 0 {
		t.Fatalf("want exactly nine observations and no omission, got %v\n%s", got, buf)
	}
}

// FR-016, US-5/1: Windows and macOS collect neither busy nor inodes — even
// when the reading source hands values for them — and do not report them as
// omissions (the core reports the skip once at startup).
func TestCollect_NoBusyNoInodesOffLinux(t *testing.T) {
	for _, goos := range []string{"windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			r := steady(
				devs(dev("C:", hostread.KindVolume, 0, 0, 0, 0, 0), dev("disk0", hostread.KindDisk, 0, 0, 0, 0, 0)),
				devs(dev("C:", hostread.KindVolume, 0, 0, 0, 0, 9000), dev("disk0", hostread.KindDisk, 0, 0, 0, 0, 9000)),
			)
			r.usage = vol(100*gib, 40*gib, 60*gib, 5000, 10) // inode counts a platform might report
			r.windowsDir = `C:\Windows`
			m, clk, buf := newTestModule(r, goos)
			primeThenAdvance(t, m, clk)
			got := collectOK(t, m)
			absent(t, got, disk.KeyBusyPct, disk.KeyRootInodesUsedPct)
			for _, k := range []string{disk.KeyRootUsedPct, disk.KeyRootAvailable, disk.KeyRootTotal, disk.KeyReadMiBps, disk.KeyWriteMiBps, disk.KeyReadIOPS, disk.KeyWriteIOPS} {
				value(t, got, k)
			}
			if omissionRecords(buf) != 0 || strings.Contains(buf.String(), "inode") {
				t.Fatalf("no omission and no inode notice expected:\n%s", buf)
			}
		})
	}
}

// FR-017, FR-018: each reading can fail alone; only when both fail — nothing
// at all could be produced — is the collection a provider failure.
func TestCollect_BothFail_ProviderError(t *testing.T) {
	r := host(healthyVolume)
	r.usageErr, r.countersErr = errRead, errRead
	m, _, buf := newTestModule(r, "linux")
	obs, err := m.Collect(context.Background())
	if err == nil || len(obs) != 0 {
		t.Fatalf("want a provider failure, got %v, %v", obs, err)
	}
	if !strings.Contains(err.Error(), "disk: nothing could be read") ||
		!strings.Contains(err.Error(), disk.KeyRootTotal) || !strings.Contains(err.Error(), disk.KeyWriteMiBps) {
		t.Fatalf("error must name the module and every key: %v", err)
	}
	if omissionRecords(buf) != 0 {
		t.Fatalf("a failure is reported by the core, not as an omission record:\n%s", buf)
	}
}

// FR-017: the counters failing costs only the I/O values.
func TestCollect_CountersFail_SpaceStillPublished(t *testing.T) {
	r := host(healthyVolume)
	r.countersErr = errRead
	m, _, _ := newTestModule(r, "linux")
	got := collectOK(t, m)
	absent(t, got, ioKeys...)
	for _, k := range []string{disk.KeyRootUsedPct, disk.KeyRootAvailable, disk.KeyRootTotal, disk.KeyRootInodesUsedPct} {
		value(t, got, k)
	}
}

// FR-020: everything omitted in one collection is reported in ONE record,
// naming each key and its reason — here an inode inconsistency and a failed
// counter reading together.
func TestCollect_OneOmissionRecord(t *testing.T) {
	r := host(vol(100*gib, 40*gib, 60*gib, 10, 20))
	r.countersErr = errRead
	m, _, buf := newTestModule(r, "linux")
	collectOK(t, m)
	if n := omissionRecords(buf); n != 1 {
		t.Fatalf("want one record, got %d:\n%s", n, buf)
	}
	for _, want := range append([]string{disk.KeyRootInodesUsedPct, errRead.Error()}, ioKeys...) {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("record lacks %q:\n%s", want, buf)
		}
	}
}

// FR-022: the debug output names the volume path and the counted devices, so
// an operator can check what was measured. Uncounted devices are not listed.
func TestCollect_DebugRecordNamesPathAndDevices(t *testing.T) {
	r := steady(devs(
		dev("nvme0n1", hostread.KindDisk, 0, 0, 0, 0, 0),
		dev("nvme0n1p1", hostread.KindPartition, 0, 0, 0, 0, 0),
		dev("sda", hostread.KindDisk, 0, 0, 0, 0, 0),
	))
	m, _, buf := newTestModule(r, "linux")
	collectOK(t, m)
	line := ""
	for l := range strings.Lines(buf.String()) {
		if strings.Contains(l, "level=DEBUG") && strings.Contains(l, "module=disk") {
			line = l
		}
	}
	if !strings.Contains(line, "path=/") || !strings.Contains(line, "devices=nvme0n1,sda") {
		t.Fatalf("want a debug record with path=/ and devices=nvme0n1,sda, got %q\n%s", line, buf)
	}
}
