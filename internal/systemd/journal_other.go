//go:build !linux

package systemd

import "os"

// JournalStream is false off Linux: there is no journal (FR-022).
func JournalStream(*os.File, func(string) string) bool { return false }
