package disk

import (
	"context"
	"fmt"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// macDataVolume is where a macOS startup disk keeps its writable data. From
// macOS 10.15, / is a sealed, read-only snapshot whose "used" figure stays
// near constant while this volume fills the shared container (FR-006).
const macDataVolume = "/System/Volumes/Data"

// systemVolume names the filesystem the OS itself lives on (FR-006).
func (m *Module) systemVolume(ctx context.Context, goos string) (string, error) {
	switch goos {
	case "windows":
		dir, err := m.Reader.WindowsDir(ctx)
		if err != nil {
			return "", err
		}
		return driveRoot(dir)
	case "darwin":
		if m.Reader.DirExists(macDataVolume) {
			return macDataVolume, nil
		}
		return "/", nil
	default:
		return "/", nil
	}
}

// driveRoot turns the Windows directory into the root of its volume:
// `C:\Windows` → `C:\`. It is written out rather than left to
// filepath.VolumeName, which only understands drive letters when compiled for
// Windows, so that FR-006 is testable on any platform (NFR-004).
func driveRoot(dir string) (string, error) {
	if len(dir) >= 2 && dir[1] == ':' {
		if c := dir[0] | 0x20; c >= 'a' && c <= 'z' {
			return strings.ToUpper(dir[:1]) + `:\`, nil
		}
	}
	return "", fmt.Errorf("windows directory %q: %w", dir, errNoDrive)
}

// space turns one volume reading into the root_* observations (FR-005…FR-010).
// Everything comes from the same reading and from exact byte and inode counts.
func (m *Module) space(u hostread.VolumeUsage, withInodes bool, om *module.Omissions) []module.Observation {
	var obs []module.Observation
	if u.Total == 0 || u.Used+u.Available == 0 {
		addAll(om, errEmptyVolume, KeyRootUsedPct, KeyRootAvailable, KeyRootTotal)
	} else {
		obs = append(obs,
			// FR-007: df's formula; a superuser reserve is neither used nor
			// available.
			module.Observation{Key: KeyRootUsedPct, Value: pct(float64(u.Used), float64(u.Used+u.Available))},
			module.Observation{Key: KeyRootAvailable, Value: floorGiB2(u.Available)},
			module.Observation{Key: KeyRootTotal, Value: floorGiB2(u.Total)})
	}
	if !withInodes {
		return obs
	}
	switch {
	case u.InodesTotal == 0:
		// FR-009: no inode table (btrfs). Not a failure, and said only once.
		m.inodeNotice.Do(func() {
			m.logger().Info("no inode limit on the system volume; inode usage is not published",
				"module", Name, "path", u.Path)
		})
	case u.InodesFree > u.InodesTotal:
		om.Add(KeyRootInodesUsedPct, errInodesInconsistent)
	default:
		obs = append(obs, module.Observation{
			Key:   KeyRootInodesUsedPct,
			Value: pct(float64(u.InodesTotal-u.InodesFree), float64(u.InodesTotal)),
		})
	}
	return obs
}

// floorGiB2 expresses bytes in GiB rounded DOWN to two decimal places
// (FR-008), in integer arithmetic so that nothing rounds up: 1 GiB − 1 byte is
// 0.99. The one float division at the end is correctly rounded, so the result
// prints as exactly two decimals.
func floorGiB2(b uint64) float64 {
	const gib = 1 << 30
	hundredths := (b/gib)*100 + (b%gib)*100/gib
	return float64(hundredths) / 100
}
