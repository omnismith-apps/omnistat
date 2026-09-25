//go:build !windows

package winsvc

import "context"

// IsService is false off Windows: there is no service manager to run under.
func IsService() bool { return false }

// Main is the program as the Windows service runs it.
type Main func(ctx context.Context, events Sink, configPath string) int

// Run is never reached off Windows (IsService is false).
func Run(Main) int { return 1 }
