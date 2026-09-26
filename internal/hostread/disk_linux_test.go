package hostread

import (
	"os"
	"path/filepath"
	"testing"
)

// Spec 008 FR-011: /sys/block lists whole block devices only; a whole device
// with a `device` link is backed by hardware (or a paravirtual disk), one
// without is a stacking or memory device. Partitions are not listed at all.
func TestClassifyIn(t *testing.T) {
	root := t.TempDir()
	for name, backed := range map[string]bool{
		"sda": true, "nvme0n1": true, "vda": true,
		"dm-0": false, "md0": false, "loop0": false, "zram0": false,
	} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if backed {
			// The real entry is a symlink into /sys/devices; any entry will do.
			if err := os.Mkdir(filepath.Join(dir, "device"), 0o750); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		name string
		want DeviceKind
	}{
		{"sda", KindDisk},
		{"nvme0n1", KindDisk},
		{"vda", KindDisk},
		{"sda1", KindPartition},
		{"nvme0n1p3", KindPartition},
		{"dm-0", KindVirtual},
		{"md0", KindVirtual},
		{"loop0", KindVirtual},
		{"zram0", KindVirtual},
	} {
		if got := classifyIn(root, tc.name); got != tc.want {
			t.Errorf("classifyIn(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
