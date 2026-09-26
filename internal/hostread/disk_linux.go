package hostread

import (
	"os"
	"path/filepath"
)

// sysBlock lists the whole block devices of the running kernel.
const sysBlock = "/sys/block"

func classify(name string) DeviceKind { return classifyIn(sysBlock, name) }

// classifyIn classifies a /proc/diskstats name against a /sys/block tree
// (spec 008 FR-011). Partitions are not listed there. A whole device with a
// `device` link sits on hardware or a paravirtual disk; one without is a
// device-mapper, md, loop or memory device, whose I/O either is counted on
// the devices beneath it or never reaches a disk.
func classifyIn(root, name string) DeviceKind {
	dir := filepath.Join(root, name)
	if _, err := os.Stat(dir); err != nil {
		return KindPartition
	}
	if _, err := os.Stat(filepath.Join(dir, "device")); err == nil {
		return KindDisk
	}
	return KindVirtual
}
