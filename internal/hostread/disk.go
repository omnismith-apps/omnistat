package hostread

import (
	"context"
	"fmt"
	"os"
	"sort"

	gdisk "github.com/shirou/gopsutil/v4/disk"
)

// VolumeUsage is one reading of a mounted filesystem, in bytes and inodes.
// Every field comes from the same call.
type VolumeUsage struct {
	// Path is the path that was read.
	Path string
	// Total is the size the OS reports for the filesystem.
	Total uint64
	// Used is the space the filesystem reports as allocated.
	Used uint64
	// Available is the space an unprivileged program can still use: it
	// excludes a superuser reserve where the filesystem keeps one. On Windows
	// it is the volume's free space, which is the same thing without disk
	// quotas.
	Available uint64
	// InodesTotal and InodesFree are the filesystem's inode counts. A zero
	// total means the filesystem reports no fixed inode table (btrfs) or has
	// none at all (NTFS); judging that is the caller's job.
	InodesTotal uint64
	InodesFree  uint64
}

// DeviceKind says what a line of I/O counters describes, so that a consumer
// can count each I/O once (spec 008 FR-011). hostread reports the kind; which
// kinds to count is the consumer's decision.
type DeviceKind int

// The device kinds hostread reports.
const (
	// KindDisk is a whole disk: on Linux a whole block device backed by a
	// device (SATA, NVMe, virtio, …); on macOS a whole physical medium.
	KindDisk DeviceKind = iota + 1
	// KindPartition is a Linux partition, whose I/O its disk also counts.
	KindPartition
	// KindVirtual is a Linux block device with no device of its own beneath
	// it: device-mapper, md RAID, loop, RAM and compressed-RAM devices.
	KindVirtual
	// KindVolume is a Windows fixed volume with a drive letter; Windows keeps
	// its counters per volume, not per disk.
	KindVolume
)

func (k DeviceKind) String() string {
	switch k {
	case KindDisk:
		return "disk"
	case KindPartition:
		return "partition"
	case KindVirtual:
		return "virtual"
	case KindVolume:
		return "volume"
	}
	return fmt.Sprintf("DeviceKind(%d)", int(k))
}

// DiskCounters is one device's cumulative I/O counters, as the OS keeps them.
// Only differences between two readings mean anything.
type DiskCounters struct {
	Name       string
	Kind       DeviceKind
	ReadBytes  uint64
	WriteBytes uint64
	// ReadOps and WriteOps are completed operations. Windows keeps them in 32
	// bits, so they wrap.
	ReadOps  uint64
	WriteOps uint64
	// BusyMillis is the time the device had I/O in progress (Linux io_time).
	//
	// It is NOT maintained by the OS everywhere: on macOS gopsutil sums read
	// time and write time, which double-counts overlapping I/O, and on Windows
	// it is never set. Callers gate it to Linux (spec 008 FR-016).
	BusyMillis uint64
}

// Disk reads filesystem space and disk I/O counters.
//
// gopsutil's UsedPercent and InodesUsedPercent are deliberately not exposed:
// they are its arithmetic, and the formulas are the consumer's (spec 008
// FR-007, FR-009).
type Disk struct{}

// Usage reads the filesystem mounted at path.
func (Disk) Usage(ctx context.Context, path string) (VolumeUsage, error) {
	u, err := gdisk.UsageWithContext(ctx, path)
	if err != nil {
		return VolumeUsage{}, fmt.Errorf("usage of %s: %w", path, err)
	}
	if u == nil {
		return VolumeUsage{}, fmt.Errorf("usage of %s: no reading", path)
	}
	return VolumeUsage{
		Path:        path,
		Total:       u.Total,
		Used:        u.Used,
		Available:   u.Free,
		InodesTotal: u.InodesTotal,
		InodesFree:  u.InodesFree,
	}, nil
}

// Counters reads the cumulative I/O counters of every block device the OS
// reports, each classified by kind, sorted by name. On Linux that includes
// partitions and stacked devices, whose I/O is also counted beneath them.
func (Disk) Counters(ctx context.Context) ([]DiskCounters, error) {
	m, err := gdisk.IOCountersWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("disk counters: %w", err)
	}
	out := make([]DiskCounters, 0, len(m))
	for name, c := range m {
		out = append(out, DiskCounters{
			Name:       name,
			Kind:       classify(name),
			ReadBytes:  c.ReadBytes,
			WriteBytes: c.WriteBytes,
			ReadOps:    c.ReadCount,
			WriteOps:   c.WriteCount,
			BusyMillis: c.IoTime,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// WindowsDir returns the directory Windows is installed in. Elsewhere it
// returns an error wrapping errors.ErrUnsupported.
func (Disk) WindowsDir(context.Context) (string, error) {
	return windowsDir()
}

// DirExists reports whether path exists and is a directory.
func (Disk) DirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
