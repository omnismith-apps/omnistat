package upgrade

import (
	"os"
	"path/filepath"
)

// StagePrefix names upgrade's staging directories.
const StagePrefix = ".omnistat-upgrade-"

// Stage creates a new staging directory in dir, which is the installed
// binary's: only root (Administrators) can write there, and programs can run
// from it, unlike a noexec /tmp (FR-011). Leftovers of an interrupted upgrade
// are removed first. The directory is created 0700; cleanup removes it.
func Stage(dir string) (path string, cleanup func(), err error) {
	if old, _ := filepath.Glob(filepath.Join(dir, StagePrefix+"*")); len(old) > 0 {
		for _, o := range old {
			_ = os.RemoveAll(o)
		}
	}
	path, err = os.MkdirTemp(dir, StagePrefix)
	if err != nil {
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(path) }, nil
}
