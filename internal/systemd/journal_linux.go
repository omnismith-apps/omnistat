//go:build linux

package systemd

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// JournalStream reports whether f is the stream systemd connected to the
// journal: JOURNAL_STREAM holds its "device:inode", which is compared with f
// itself, so a stderr redirected elsewhere is not mistaken for it (FR-022).
func JournalStream(f *os.File, getenv func(string) string) bool {
	dev, ino, ok := strings.Cut(getenv("JOURNAL_STREAM"), ":")
	if !ok {
		return false
	}
	d, err1 := strconv.ParseUint(dev, 10, 64)
	i, err2 := strconv.ParseUint(ino, 10, 64)
	if err1 != nil || err2 != nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && uint64(st.Dev) == d && uint64(st.Ino) == i //nolint:unconvert // Dev and Ino are narrower on some architectures
}
