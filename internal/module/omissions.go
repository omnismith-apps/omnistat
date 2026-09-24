package module

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Omissions gathers the observations a collection could not produce, so that
// a provider reports them in one record rather than one per value (spec 004
// FR-016, spec 005 FR-013, ADR-0009). The zero value is ready to use; it is
// meant to live for one Collect call.
type Omissions struct {
	keys    []string
	reasons []string
}

// Add records that the observation for key was not produced, and why.
func (o *Omissions) Add(key string, err error) {
	o.keys = append(o.keys, key)
	o.reasons = append(o.reasons, key+": "+err.Error())
}

// Len is the number of omissions recorded.
func (o *Omissions) Len() int { return len(o.keys) }

// Log writes the one record for this collection, or nothing if nothing was
// omitted.
func (o *Omissions) Log(log *slog.Logger, module string) {
	if len(o.keys) == 0 {
		return
	}
	log.Error("observations omitted", "module", module,
		"keys", strings.Join(o.keys, ","), "reasons", strings.Join(o.reasons, "; "))
}

// Err is the provider failure for a collection that could read nothing at all
// (spec 004 FR-015, spec 005 FR-012, spec 003 FR-010).
func (o *Omissions) Err(module string) error {
	if len(o.reasons) == 0 {
		return errors.New(module + ": nothing could be read")
	}
	return fmt.Errorf("%s: nothing could be read: %s", module, strings.Join(o.reasons, "; "))
}
