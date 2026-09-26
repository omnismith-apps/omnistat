//go:build !linux && !windows

package hostread

import (
	"context"
	"errors"
)

// macOS and the rest: nothing is read (spec 010 FR-018). gopsutil's darwin
// interface counters run `netstat`, which omnistat never does.

func netInterfaces(context.Context) ([]IfaceCounters, error) { return nil, errors.ErrUnsupported }
func netStack(context.Context) (StackCounters, error)        { return StackCounters{}, errors.ErrUnsupported }
func netListenDrops(context.Context) (uint64, error)         { return 0, errors.ErrUnsupported }
func netTimeWait(context.Context) (uint64, error)            { return 0, errors.ErrUnsupported }
func netConntrack(context.Context) (Conntrack, error)        { return Conntrack{}, errors.ErrUnsupported }
