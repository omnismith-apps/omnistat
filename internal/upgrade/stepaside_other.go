//go:build !windows

package upgrade

// removeAtRestart: elsewhere a running binary can be deleted, and Stage's
// cleanup already removes it.
func removeAtRestart(...string) error { return nil }
