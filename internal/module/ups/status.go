package ups

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// STATFLAG bits (apcupsd 3.14 include/defines.h). The flags come from STATFLAG,
// not from the STATUS text, which COMMLOST and SHUTTING DOWN replace wholesale
// (spec 012 FR-014; plan, technical context).
const (
	flagOnBattery   = 0x00000010
	flagOverload    = 0x00000020
	flagBatteryLow  = 0x00000040
	flagReplaceBatt = 0x00000080
	flagCommLost    = 0x00000100
)

// selftestOptions maps apcupsd's SELFTEST codes to the list options of
// spec 012 FR-002. NO (no result lately) and ?? (unknown) publish nothing
// (FR-012) and are absent here on purpose.
var selftestOptions = map[string]string{
	"OK": "passed",
	"BT": "failed_capacity",
	"NG": "failed",
	"WN": "warning",
	"IP": "in_progress",
}

// status is one parsed NIS status reply: KEY → VALUE, both trimmed.
type status map[string]string

// parseStatus splits each `KEY : VALUE` line at its first colon (spec 012
// FR-010). A reply with no such line at all is apcupsd's error text
// ("Apcupsd internal error", "Invalid command") and fails the collection.
func parseStatus(lines []string) (status, error) {
	st := status{}
	var other []string
	for _, l := range lines {
		k, v, ok := strings.Cut(l, ":")
		if !ok || strings.TrimSpace(k) == "" {
			if t := strings.TrimSpace(l); t != "" {
				other = append(other, t)
			}
			continue
		}
		st[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if len(st) == 0 {
		if len(other) > 0 {
			return nil, fmt.Errorf("apcupsd answered %q", strings.Join(other, " "))
		}
		return nil, errors.New("apcupsd sent an empty reply")
	}
	if _, ok := st["STATFLAG"]; !ok {
		if _, ok := st["STATUS"]; !ok {
			return nil, errors.New("the reply is not an apcupsd status (no STATUS or STATFLAG)")
		}
	}
	return st, nil
}

// text returns a field, and whether the UPS reports it.
func (s status) text(key string) (string, bool) {
	v, ok := s[key]
	return v, ok && v != ""
}

// number reads `<number> <unit>[ …]`, refusing any other unit (FR-010): a
// value with an unexpected unit is never converted or guessed.
func (s status) number(key, unit string) (float64, bool, error) {
	v, ok := s.text(key)
	if !ok {
		return 0, false, nil
	}
	fields := strings.Fields(v)
	if len(fields) < 2 {
		return 0, true, fmt.Errorf("%s %q has no unit (want %s)", key, v, unit)
	}
	if fields[1] != unit {
		return 0, true, fmt.Errorf("%s %q is not in %s", key, v, unit)
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, true, fmt.Errorf("%s %q is not a number", key, v)
	}
	return f, true, nil
}

// flags reads STATFLAG, the raw status bits.
func (s status) flags() (uint32, bool, error) {
	v, ok := s.text("STATFLAG")
	if !ok {
		return 0, false, nil
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(v), "0x"), 16, 32)
	if err != nil {
		return 0, true, fmt.Errorf("STATFLAG %q is not a hexadecimal number", v)
	}
	return uint32(n), true, nil
}

// apcupsd writes instants as strftime "%Y-%m-%d %H:%M:%S %z".
const instantLayout = "2006-01-02 15:04:05 -0700"

// instant reads a date and time with its UTC offset; "N/A" is absent.
func (s status) instant(key string) (time.Time, bool, error) {
	v, ok := s.text(key)
	if !ok || v == "N/A" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(instantLayout, v)
	if err != nil {
		return time.Time{}, true, fmt.Errorf("%s %q is not a date and time", key, v)
	}
	return t, true, nil
}

// date reads a calendar date: YYYY-MM-DD from the USB and modbus drivers,
// or the UPS's own MM/DD/YY from Smart-UPS models (plan, technical context).
func (s status) date(key string) (time.Time, bool, error) {
	v, ok := s.text(key)
	if !ok || v == "N/A" {
		return time.Time{}, false, nil
	}
	for _, layout := range []string{"2006-01-02", "01/02/06", "01/02/2006"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true, nil
		}
	}
	return time.Time{}, true, fmt.Errorf("%s %q is not a date", key, v)
}

// selftest reads SELFTEST as a list option. publish is false for NO and ??
// (FR-012); an unknown code is an error.
func (s status) selftest() (option string, present, publish bool, err error) {
	v, ok := s.text("SELFTEST")
	if !ok {
		return "", false, false, nil
	}
	if v == "NO" || v == "??" {
		return "", true, false, nil
	}
	if o, known := selftestOptions[v]; known {
		return o, true, true, nil
	}
	return "", true, false, fmt.Errorf("SELFTEST code %q is unknown", v)
}
