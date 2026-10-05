// Package ups is the `ups` module (spec 012): the state, battery and load of a
// UPS, read from apcupsd's Network Information Server and published as the
// UPS's own entity, linked to the host that reads it (spec 011).
package ups

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
)

const (
	// Name is the module's name.
	Name = "ups"
	// Template is the entity template's manifest slug (FR-001).
	Template = "ups"
	// DefaultAddress is apcupsd's NIS on the same host (FR-005).
	DefaultAddress = "127.0.0.1:3551"
	// DefaultInterval is the collection cadence (FR-004).
	DefaultInterval = 10 * time.Second
)

// Module is the `ups` module: manifest, settings and provider.
type Module struct {
	// Dial opens the connection to the NIS; tests replace it.
	Dial Dialer
	// Log receives the module's notices; nil means slog.Default().
	Log *slog.Logger

	mu       sync.Mutex
	address  string
	identity string
	missing  string // attribute keys last reported as not reported by the UPS
	greeted  bool   // the first success was logged (FR-022)
}

// New returns the module with its defaults.
func New() *Module {
	var d net.Dialer
	return &Module{Dial: d.DialContext, address: DefaultAddress}
}

var (
	_ module.Module       = (*Module)(nil)
	_ module.Provider     = (*Module)(nil)
	_ module.Configurable = (*Module)(nil)
	_ module.Describer    = (*Module)(nil)
)

// Name implements module.Module.
func (m *Module) Name() string { return Name }

// DefaultInterval implements module.Provider (FR-004).
func (m *Module) DefaultInterval() time.Duration { return DefaultInterval }

var linux = []string{"linux"} // FR-020

// Manifest implements module.Module (FR-001, FR-002).
func (m *Module) Manifest() manifest.Manifest {
	attr := func(key, slug string, kind manifest.Kind, name, desc string) manifest.Attribute {
		return manifest.Attribute{Key: key, Name: name, Slug: slug, Kind: kind, Description: desc, Template: Template, Platforms: linux}
	}
	selftest := attr("selftest", "ups_selftest", manifest.KindList, "Self-test result", "Result of the UPS's most recent self-test, as apcupsd reports it")
	selftest.Options = []string{"passed", "failed_capacity", "failed", "warning", "in_progress"}
	return manifest.Manifest{
		Module: Name,
		Templates: []manifest.Template{{Slug: Template, Name: "UPS", Entity: true,
			Description: "An uninterruptible power supply, read through apcupsd by the host it is linked to"}},
		Attributes: []manifest.Attribute{
			{Key: "host", Name: "Managed by", Slug: "ups_host", Kind: manifest.KindReference, Template: Template, Target: manifest.HostTemplate,
				Description: "The host whose omnistat reads this UPS"},
			attr("name", "ups_name", manifest.KindText, "UPS name", "The name stored in the UPS or set in apcupsd's configuration"),
			attr("model", "ups_model", manifest.KindText, "Model", "The UPS model"),
			attr("serial", "ups_serial", manifest.KindText, "Serial number", "The serial number the UPS reports"),
			attr("battery_date", "ups_battery_date", manifest.KindDate, "Battery date", "The battery date the UPS reports (replacement or manufacture date, depending on the model)"),
			attr("on_battery", "ups_on_battery", manifest.KindBoolean, "On battery", "The UPS is supplying its load from the battery"),
			attr("battery_low", "ups_battery_low", manifest.KindBoolean, "Battery low", "The UPS reports its battery as low"),
			attr("replace_battery", "ups_replace_battery", manifest.KindBoolean, "Replace battery", "The UPS reports that its battery needs replacing"),
			attr("comm_lost", "ups_comm_lost", manifest.KindBoolean, "Communication lost", "apcupsd has lost communication with the UPS"),
			attr("overload", "ups_overload", manifest.KindBoolean, "Overload", "The UPS reports its load above its capacity"),
			attr("last_transfer_at", "ups_last_transfer_at", manifest.KindDatetime, "Last transfer to battery", "When the UPS last switched to battery"),
			attr("last_transfer_reason", "ups_last_transfer_reason", manifest.KindText, "Last transfer reason", "apcupsd's reason for the last transfer to battery (e.g. low line voltage, self-test)"),
			selftest,
			attr("battery_charge_pct", "ups_battery_charge_pct", manifest.KindMetric, "Battery charge", "Battery charge, in percent"),
			attr("runtime_min", "ups_runtime_min", manifest.KindMetric, "Runtime left", "Runtime on battery at the current load, as the UPS estimates it, in minutes"),
			attr("load_pct", "ups_load_pct", manifest.KindMetric, "Load", "Load, in percent of the UPS's capacity"),
			attr("load_w", "ups_load_w", manifest.KindMetric, "Load (estimated watts)", "Estimated load in watts: load percentage × the UPS's nominal power; an estimate, not a measurement"),
			attr("input_v", "ups_input_v", manifest.KindMetric, "Input voltage", "Line (input) voltage, in volts"),
			attr("output_v", "ups_output_v", manifest.KindMetric, "Output voltage", "Output voltage, in volts"),
			attr("battery_v", "ups_battery_v", manifest.KindMetric, "Battery voltage", "Battery voltage, in volts"),
			attr("temp_c", "ups_temp_c", manifest.KindMetric, "Internal temperature", "Temperature inside the UPS, in °C"),
			attr("input_hz", "ups_input_hz", manifest.KindMetric, "Input frequency", "Line frequency, in Hz"),
		},
	}
}

// Settings implements module.Configurable (FR-005).
func (m *Module) Settings() []string { return []string{"address", "identity"} }

// Configure implements module.Configurable (FR-005): an absent key restores
// its default.
func (m *Module) Configure(s map[string]string) error {
	addr := DefaultAddress
	if v, ok := s["address"]; ok {
		if err := validAddress(v); err != nil {
			return fmt.Errorf("address: %w", err)
		}
		addr = v
	}
	id := ""
	if v, ok := s["identity"]; ok {
		if err := validIdentity(v); err != nil {
			return fmt.Errorf("identity: %w", err)
		}
		id = strings.TrimSpace(v)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.address, m.identity = addr, id
	return nil
}

// Describe implements module.Describer (FR-021).
func (m *Module) Describe() []any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return []any{"address", m.address}
}

// validAddress accepts host:port with a host name, an IPv4 address or a
// bracketed IPv6 address, and a port from 1 to 65535 (FR-005).
func validAddress(v string) error {
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return fmt.Errorf("%q is not host:port: %w", v, err)
	}
	if host == "" {
		return fmt.Errorf("%q has no host", v)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("%q: port must be 1–65535", v)
	}
	return nil
}

// validIdentity is the key rule of spec 011 FR-008 (1–128 characters after
// trimming, no control characters).
func validIdentity(v string) error {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return errors.New("must not be empty")
	case len([]rune(v)) > 128:
		return errors.New("must be at most 128 characters")
	case strings.IndexFunc(v, unicode.IsControl) >= 0:
		return errors.New("must not contain control characters")
	}
	return nil
}

func (m *Module) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Collect implements module.Provider: one NIS exchange, then every value
// taken from its own field (FR-009…FR-017).
func (m *Module) Collect(ctx context.Context) ([]module.Observation, error) {
	m.mu.Lock()
	addr, static := m.address, m.identity
	m.mu.Unlock()

	lines, err := readStatus(ctx, m.Dial, addr)
	if err != nil {
		return nil, fmt.Errorf("apcupsd at %s: %w", addr, err)
	}
	st, err := parseStatus(lines)
	if err != nil {
		return nil, fmt.Errorf("apcupsd at %s: %w", addr, err)
	}

	serial, hasSerial := st.text("SERIALNO")
	key := static
	if key == "" {
		if !hasSerial {
			return nil, errors.New("the UPS reports no serial number to identify it by; set modules.ups.identity")
		}
		key = serial
		if err := validIdentity(key); err != nil {
			return nil, fmt.Errorf("the UPS serial number cannot identify it (%w); set modules.ups.identity", err)
		}
	}
	c := collection{ent: module.Entity{Template: Template, Key: key}}

	// Identity dimensions: published even while communication is lost.
	c.text(st, "name", "UPSNAME")
	c.text(st, "model", "MODEL")
	if hasSerial {
		c.add("serial", serial)
	}
	if d, ok, err := st.date("BATTDATE"); err != nil {
		c.omit("battery_date", err)
	} else if ok {
		c.add("battery_date", d)
	} else {
		c.missingKey("battery_date")
	}

	flags, hasFlags, err := st.flags()
	switch {
	case err != nil:
		c.omit("comm_lost", err)
	case !hasFlags:
		c.omit("comm_lost", errors.New("STATFLAG missing"))
	case flags&flagCommLost != 0:
		// Every other bit and reading is the last known one (FR-014).
		c.add("comm_lost", true)
		return m.finish(c, addr, key, static != "")
	default:
		c.add("comm_lost", false)
		c.add("on_battery", flags&flagOnBattery != 0)
		c.add("battery_low", flags&flagBatteryLow != 0)
		c.add("replace_battery", flags&flagReplaceBatt != 0)
		c.add("overload", flags&flagOverload != 0)
	}

	// The last transfer, only as a pair, only with a valid time (FR-013).
	switch at, ok, err := st.instant("XONBATT"); {
	case err != nil:
		c.omit("last_transfer_at", err)
	case ok:
		if reason, has := st.text("LASTXFER"); has {
			c.add("last_transfer_at", at)
			c.add("last_transfer_reason", reason)
		} else {
			c.missingKey("last_transfer_reason")
		}
	}

	switch opt, present, publish, err := st.selftest(); {
	case err != nil:
		c.omit("selftest", err)
	case !present:
		c.missingKey("selftest")
	case publish:
		c.add("selftest", opt)
	}

	for _, r := range []struct{ key, field, unit string }{
		{"battery_charge_pct", "BCHARGE", "Percent"},
		{"runtime_min", "TIMELEFT", "Minutes"},
		{"load_pct", "LOADPCT", "Percent"},
		{"input_v", "LINEV", "Volts"},
		{"output_v", "OUTPUTV", "Volts"},
		{"battery_v", "BATTV", "Volts"},
		{"temp_c", "ITEMP", "C"},
		{"input_hz", "LINEFREQ", "Hz"},
	} {
		c.number(st, r.key, r.field, r.unit)
	}
	load, hasLoad, lerr := st.number("LOADPCT", "Percent")
	nominal, hasNominal, nerr := st.number("NOMPOWER", "Watts")
	switch {
	case lerr != nil || nerr != nil:
		c.omit("load_w", errors.Join(lerr, nerr))
	case hasLoad && hasNominal:
		c.add("load_w", math.Round(load*nominal/100)) // FR-011
	default:
		c.missingKey("load_w")
	}
	return m.finish(c, addr, key, static != "")
}

// finish logs this collection's omissions and, when they change, the values
// the UPS does not report; and the first success.
func (m *Module) finish(c collection, addr, key string, static bool) ([]module.Observation, error) {
	log := m.log()
	c.omissions.Log(log, Name)
	missing := strings.Join(c.sortedMissing(), ",")
	m.mu.Lock()
	notice := missing != m.missing && missing != ""
	m.missing = missing
	greet := !m.greeted && len(c.obs) > 0
	if greet {
		m.greeted = true
	}
	m.mu.Unlock()
	if notice {
		// A steady state, not an omission (FR-015).
		log.Info("values this UPS does not report", "module", Name, "keys", missing)
	}
	if greet {
		source := "serial number"
		if static {
			source = "modules.ups.identity"
		}
		log.Info("UPS found", "module", Name, "address", addr, "model", c.model, "serial", c.serial, "key", key, "key_source", source) // FR-022
	}
	if len(c.obs) == 0 {
		return nil, c.omissions.Err(Name)
	}
	return c.obs, nil
}

// collection accumulates one Collect's observations, omissions and the
// attributes the UPS does not report.
type collection struct {
	ent       module.Entity
	obs       []module.Observation
	omissions module.Omissions
	missing   []string
	model     string
	serial    string
}

func (c *collection) add(key string, v any) {
	c.obs = append(c.obs, module.Observation{Key: key, Value: v, Entity: c.ent})
	switch key {
	case "model":
		c.model, _ = v.(string)
	case "serial":
		c.serial, _ = v.(string)
	}
}

func (c *collection) omit(key string, err error) { c.omissions.Add(key, err) }

func (c *collection) missingKey(key string) { c.missing = append(c.missing, key) }

func (c *collection) sortedMissing() []string {
	out := append([]string(nil), c.missing...)
	sort.Strings(out)
	return out
}

func (c *collection) text(st status, key, field string) {
	if v, ok := st.text(field); ok {
		c.add(key, v)
		return
	}
	c.missingKey(key)
}

func (c *collection) number(st status, key, field, unit string) {
	switch v, ok, err := st.number(field, unit); {
	case err != nil:
		c.omit(key, err)
	case ok:
		c.add(key, v)
	default:
		c.missingKey(key)
	}
}
