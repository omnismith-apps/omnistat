package upgrade

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// RunsFrom reports whether upgrade runs from the installed binary on Windows,
// where that file cannot be replaced while upgrade waits for the install
// (spec 009 edge case). Paths compare case-insensitively there.
func RunsFrom(self, installed, goos string) bool {
	return goos == "windows" && strings.EqualFold(filepath.Clean(self), filepath.Clean(installed))
}

// Aside is the running binary, moved out of the installed path.
type Aside struct {
	self, moved, stage string
}

// StepAside moves the running binary self into the staging directory, on the
// same volume, so the install it hands over to can put the new binary at
// self's path. A running program cannot be replaced on Windows, but it can be
// renamed, as uninstall does (006 FR-020). Whatever version the new binary
// is, even one older than this feature, its install then finds the path free.
func StepAside(self, stage string) (*Aside, error) {
	moved := filepath.Join(stage, "omnistat-previous"+filepath.Ext(self))
	if err := os.Rename(self, moved); err != nil {
		return nil, err
	}
	return &Aside{self: self, moved: moved, stage: stage}, nil
}

// Moved is where the running binary is now.
func (a *Aside) Moved() string { return a.moved }

// Finish runs after the install: when it left no binary at the installed
// path, the previous one is put back, so the service never loses its program.
// Otherwise the previous one, still running until upgrade exits, is removed
// at the next restart with its directory (Windows); a later upgrade removes
// it too (Stage). restored reports that it was put back.
func (a *Aside) Finish() (restored bool, err error) {
	if _, err := os.Stat(a.self); errors.Is(err, os.ErrNotExist) {
		return true, os.Rename(a.moved, a.self)
	}
	return false, removeAtRestart(a.moved, a.stage)
}
