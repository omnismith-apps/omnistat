// Package collect turns providers into stamped, typed, buffered samples on a
// schedule the core owns (spec 003 FR-002, FR-006…011, FR-014; ADR-0005).
// Everything here is pure: no SDK, no wall clock unless RealClock is used.
package collect

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// Validate coerces an observation's value to the canonical Go type of the
// attribute's kind (FR-002): text/list → string, number/metric → float64,
// boolean → bool, date/datetime → time.Time. Anything else is an error.
func Validate(a manifest.DesiredAttribute, v any) (any, error) {
	if v == nil {
		return nil, errors.New("nil value")
	}
	switch a.Kind {
	case manifest.KindText:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("text wants a string, got %T", v)
		}
		return s, nil
	case manifest.KindNumber, manifest.KindMetric:
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("%s wants a number, got %T", a.Kind, v)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%s value %v is not finite", a.Kind, f)
		}
		return f, nil
	case manifest.KindBoolean:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("boolean wants a bool, got %T", v)
		}
		return b, nil
	case manifest.KindDate, manifest.KindDatetime:
		t, ok := v.(time.Time)
		if !ok {
			return nil, fmt.Errorf("%s wants a time, got %T", a.Kind, v)
		}
		if t.IsZero() {
			return nil, fmt.Errorf("%s value is the zero time", a.Kind)
		}
		return t, nil
	case manifest.KindList:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("list wants an option string, got %T", v)
		}
		for _, o := range a.Options {
			if o == s {
				return s, nil
			}
		}
		return nil, fmt.Errorf("list value %q is not one of %s", s, strings.Join(a.Options, ", "))
	}
	return nil, fmt.Errorf("unknown kind %q", a.Kind)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}
