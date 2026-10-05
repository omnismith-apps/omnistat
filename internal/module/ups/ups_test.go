package ups

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// newTestModule is the module pointed at a fake NIS serving reply, logging
// into the returned buffer.
func newTestModule(t *testing.T, reply string, settings map[string]string) (*Module, *fakeNIS, *bytes.Buffer) {
	t.Helper()
	srv := newFakeNIS(t, reply)
	m := New()
	if settings == nil {
		settings = map[string]string{}
	}
	settings["address"] = srv.addr()
	if err := m.Configure(settings); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	m.Log = slog.New(slog.NewTextHandler(&logs, nil))
	return m, srv, &logs
}

func values(t *testing.T, obs []module.Observation, wantKey string) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, o := range obs {
		if o.Entity != (module.Entity{Template: "ups", Key: wantKey}) {
			t.Fatalf("observation %s targets %+v, want ups/%s", o.Key, o.Entity, wantKey)
		}
		if _, dup := out[o.Key]; dup {
			t.Fatalf("observation %s twice", o.Key)
		}
		out[o.Key] = o.Value
	}
	return out
}

func at(s string) time.Time {
	t, err := time.Parse(instantLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

// spec 012 US-1/1, US-2/1, FR-006, FR-010…FR-015: a USB Back-UPS on mains.
func TestCollect_USBOnMains(t *testing.T) {
	m, _, logs := newTestModule(t, usbOnline, nil)
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := values(t, obs, "4B1234P56789")
	want := map[string]any{
		"name": "Back-UPS XS 950U", "model": "Back-UPS XS 950U", "serial": "4B1234P56789",
		"battery_date": time.Date(2023, 5, 14, 0, 0, 0, 0, time.UTC),
		"comm_lost":    false, "on_battery": false, "battery_low": false, "replace_battery": false, "overload": false,
		"last_transfer_at": at("2026-10-04 18:02:11 +0300"), "last_transfer_reason": "Low line voltage",
		"battery_charge_pct": 100.0, "runtime_min": 48.7, "load_pct": 12.0, "input_v": 232.0, "battery_v": 13.6,
		"load_w": 58.0, // 12% of 480 W = 57.6, rounded (FR-011)
	}
	for k, v := range want {
		g, ok := got[k]
		if tv, isTime := v.(time.Time); isTime {
			if gt, _ := g.(time.Time); !ok || !gt.Equal(tv) {
				t.Errorf("%s = %v, want %v", k, g, v)
			}
			continue
		}
		if !ok || g != v {
			t.Errorf("%s = %v, want %v", k, g, v)
		}
	}
	for _, absent := range []string{"selftest", "output_v", "temp_c", "input_hz"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s published although the UPS does not report it (or reports NO)", absent)
		}
	}
	if len(got) != len(want) {
		t.Errorf("published %d values, want %d: %v", len(got), len(want), got)
	}
	out := logs.String()
	if !strings.Contains(out, `msg="values this UPS does not report" module=ups keys=input_hz,output_v,temp_c`) {
		t.Errorf("missing-values notice:\n%s", out)
	}
	if !strings.Contains(out, `msg="UPS found" module=ups`) || !strings.Contains(out, "serial=4B1234P56789") || !strings.Contains(out, `key_source="serial number"`) {
		t.Errorf("first-success record (FR-022):\n%s", out)
	}
	if strings.Contains(out, "omitted") {
		t.Errorf("nothing was invalid:\n%s", out)
	}

	// The notice and the greeting are once per process, not per collection.
	if _, err := m.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Count(logs.String(), "does not report") != 1 || strings.Count(logs.String(), "UPS found") != 1 {
		t.Fatalf("repeated notices:\n%s", logs.String())
	}
}

// spec 012 US-1/2, FR-002, FR-010, FR-012: a Smart-UPS on battery, with every
// reading, its own battery date format and a passed self-test.
func TestCollect_SmartOnBattery(t *testing.T) {
	m, _, logs := newTestModule(t, smartOnBattery, nil)
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := values(t, obs, "AS1234567890")
	if got["on_battery"] != true || got["battery_low"] != false || got["selftest"] != "passed" ||
		got["temp_c"] != 29.2 || got["input_hz"] != 50.0 || got["output_v"] != 230.4 || got["input_v"] != 0.0 || got["load_w"] != 309.0 {
		t.Fatalf("values: %v", got)
	}
	if d := got["battery_date"].(time.Time); !d.Equal(time.Date(2023, 5, 14, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("battery date: %v", d)
	}
	if strings.Contains(logs.String(), "does not report") {
		t.Fatalf("this UPS reports everything:\n%s", logs.String())
	}
}

// spec 012 FR-014: the flags come from STATFLAG. A shutdown replaces the
// STATUS text, yet the UPS is still on battery with a low battery.
func TestCollect_ShuttingDown(t *testing.T) {
	reply := strings.Replace(smartOnBattery, "STATUS   : ONBATT ", "STATUS   : SHUTTING DOWN", 1)
	reply = strings.Replace(reply, "STATFLAG : 0x05060010", "STATFLAG : 0x05060250", 1)
	m, _, _ := newTestModule(t, reply, nil)
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := values(t, obs, "AS1234567890"); got["on_battery"] != true || got["battery_low"] != true || got["comm_lost"] != false {
		t.Fatalf("flags: %v", got)
	}
}

// spec 012 US-4/1, FR-014: with communication lost, only comm_lost and the
// identity dimensions are published; every reading is stale.
func TestCollect_CommunicationLost(t *testing.T) {
	reply := strings.Replace(usbOnline, "STATUS   : ONLINE ", "STATUS   : COMMLOST ", 1)
	reply = strings.Replace(reply, "STATFLAG : 0x05000008", "STATFLAG : 0x05000108", 1)
	m, _, _ := newTestModule(t, reply, nil)
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := values(t, obs, "4B1234P56789")
	if got["comm_lost"] != true {
		t.Fatalf("comm_lost: %v", got)
	}
	for k := range got {
		switch k {
		case "comm_lost", "name", "model", "serial", "battery_date":
		default:
			t.Errorf("stale %s published while communication is lost", k)
		}
	}
}

// spec 012 US-6, FR-006, FR-007: the key is the static identity when set;
// without it and without a serial, nothing is published.
func TestCollect_Identity(t *testing.T) {
	noSerial := strings.Replace(usbOnline, "SERIALNO : 4B1234P56789  \n", "", 1)
	m, _, _ := newTestModule(t, noSerial, nil)
	if _, err := m.Collect(context.Background()); err == nil || !strings.Contains(err.Error(), "modules.ups.identity") {
		t.Fatalf("no serial: %v", err)
	}
	m, _, logs := newTestModule(t, noSerial, map[string]string{"identity": " rack-ups-1 "})
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := values(t, obs, "rack-ups-1"); got["serial"] != nil {
		t.Fatalf("a serial was made up: %v", got["serial"])
	}
	if !strings.Contains(logs.String(), "key_source=modules.ups.identity") {
		t.Fatalf("greeting:\n%s", logs.String())
	}
	m, _, _ = newTestModule(t, usbOnline, map[string]string{"identity": "rack-ups-1"})
	obs, _ = m.Collect(context.Background())
	if got := values(t, obs, "rack-ups-1"); got["serial"] != "4B1234P56789" {
		t.Fatalf("the reported serial must still be published: %v", got)
	}
}

// spec 012 FR-010, FR-016: a value in an unexpected unit, or unparseable, is
// an omission in one record; the rest is published.
func TestCollect_InvalidValuesAreOmissions(t *testing.T) {
	reply := strings.Replace(smartOnBattery, "ITEMP    : 29.2 C", "ITEMP    : 84.6 F", 1)
	reply = strings.Replace(reply, "BCHARGE  : 87.0 Percent", "BCHARGE  : lots Percent", 1)
	reply = strings.Replace(reply, "BATTDATE : 05/14/23", "BATTDATE : sometime", 1)
	reply = strings.Replace(reply, "XONBATT  : 2026-10-05 21:19:41 +0300  ", "XONBATT  : yesterday", 1)
	m, _, logs := newTestModule(t, reply, nil)
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := values(t, obs, "AS1234567890")
	for _, k := range []string{"temp_c", "battery_charge_pct", "battery_date", "last_transfer_at", "last_transfer_reason"} {
		if _, ok := got[k]; ok {
			t.Errorf("invalid %s published", k)
		}
	}
	if got["load_pct"] != 31.5 {
		t.Errorf("valid values lost: %v", got)
	}
	out := logs.String()
	if strings.Count(out, "observations omitted") != 1 || !strings.Contains(out, "keys=battery_date,last_transfer_at,battery_charge_pct,temp_c") {
		t.Fatalf("one omission record naming each key:\n%s", out)
	}
}

// spec 012 FR-002, FR-012: every self-test code.
func TestCollect_SelfTestCodes(t *testing.T) {
	for code, want := range map[string]any{"OK": "passed", "BT": "failed_capacity", "NG": "failed", "WN": "warning", "IP": "in_progress", "NO": nil, "??": nil, "XX": nil} {
		reply := strings.Replace(smartOnBattery, "SELFTEST : OK", "SELFTEST : "+code, 1)
		m, _, logs := newTestModule(t, reply, nil)
		obs, err := m.Collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := values(t, obs, "AS1234567890")["selftest"]; got != want {
			t.Errorf("%s → %v, want %v", code, got, want)
		}
		if omitted := strings.Contains(logs.String(), "omitted"); omitted != (code == "XX") {
			t.Errorf("%s: omission logged = %v", code, omitted)
		}
	}
}

// spec 012 FR-013: no transfer since apcupsd started → no XONBATT line → the
// pair is not published, and that is not "a value the UPS does not report".
func TestCollect_NoTransferSinceStart(t *testing.T) {
	reply := strings.Replace(usbOnline, "XONBATT  : 2026-10-04 18:02:11 +0300  \n", "", 1)
	reply = strings.Replace(reply, "LASTXFER : Low line voltage", "LASTXFER : No transfers since turnon", 1)
	m, _, logs := newTestModule(t, reply, nil)
	obs, err := m.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := values(t, obs, "4B1234P56789")
	if _, ok := got["last_transfer_at"]; ok {
		t.Fatal("transfer time published")
	}
	if _, ok := got["last_transfer_reason"]; ok {
		t.Fatal("'No transfers since turnon' published as a reason")
	}
	if strings.Contains(logs.String(), "last_transfer") {
		t.Fatalf("a normal absence was logged:\n%s", logs.String())
	}
}

// spec 012 FR-017: apcupsd unreachable is a collection failure naming the
// address and the cause.
func TestCollect_Unreachable(t *testing.T) {
	m, srv, _ := newTestModule(t, "", nil)
	_ = srv.ln.Close()
	_, err := m.Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "apcupsd at "+srv.addr()) || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("got %v", err)
	}
}

// spec 012 FR-001…FR-004, FR-020: the manifest is valid, owns exactly the 22
// attributes on its entity template, Linux-only except the host link.
func TestManifest(t *testing.T) {
	m := New()
	man := m.Manifest()
	if err := manifest.Validate([]manifest.Manifest{man}); err != nil {
		t.Fatal(err)
	}
	if len(man.Attributes) != 22 || len(man.Templates) != 1 || !man.Templates[0].Entity || man.Templates[0].Slug != "ups" {
		t.Fatalf("manifest: %d attributes, templates %+v", len(man.Attributes), man.Templates)
	}
	for _, a := range man.Attributes {
		if !strings.HasPrefix(a.Slug, "ups_") || a.Template != "ups" {
			t.Errorf("%s: slug/template %s/%s", a.Key, a.Slug, a.Template)
		}
		linuxOnly := len(a.Platforms) == 1 && a.Platforms[0] == "linux"
		if a.Kind == manifest.KindReference {
			if a.Slug != "ups_host" || a.Target != manifest.HostTemplate {
				t.Errorf("host link: %+v", a)
			}
		} else if !linuxOnly {
			t.Errorf("%s: platforms %v, want linux", a.Key, a.Platforms)
		}
		if a.Key == "selftest" && strings.Join(a.Options, ",") != "passed,failed_capacity,failed,warning,in_progress" {
			t.Errorf("selftest options: %v", a.Options)
		}
	}
	if m.DefaultInterval() != 10*time.Second || m.Name() != "ups" {
		t.Fatalf("interval/name: %s %s", m.DefaultInterval(), m.Name())
	}
}

// spec 012 FR-005, FR-021: settings are validated, absent ones restore the
// defaults, and the address is described for the schedule log.
func TestConfigure(t *testing.T) {
	m := New()
	if err := m.Configure(map[string]string{"address": "[::1]:3552", "identity": "x"}); err != nil {
		t.Fatal(err)
	}
	if d := m.Describe(); len(d) != 2 || d[1] != "[::1]:3552" {
		t.Fatalf("describe: %v", d)
	}
	if err := m.Configure(nil); err != nil || m.Describe()[1] != DefaultAddress || m.identity != "" {
		t.Fatalf("defaults not restored: %v %v %q", err, m.Describe(), m.identity)
	}
	for _, bad := range []map[string]string{
		{"address": "localhost"}, {"address": ":3551"}, {"address": "host:0"}, {"address": "host:70000"}, {"address": "::1:3551"},
		{"identity": " "}, {"identity": strings.Repeat("x", 129)}, {"identity": "a\tb"},
	} {
		if err := m.Configure(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	for _, good := range []string{"127.0.0.1:3551", "nas.local:3551", "[fe80::1]:1"} {
		if err := m.Configure(map[string]string{"address": good}); err != nil {
			t.Errorf("%s rejected: %v", good, err)
		}
	}
}

// spec 012 FR-020, ADR-0007: off Linux the whole module is skipped with one
// startup record, and its schema is still declared.
func TestSkippedOffLinux(t *testing.T) {
	m := New()
	ident := module.Static{M: manifest.Manifest{Module: "ident", Attributes: []manifest.Attribute{{Key: "id", Name: "Id", Slug: "ident_id", Kind: manifest.KindText, Label: 1}}}}
	d, err := manifest.Resolve([]manifest.Manifest{ident.Manifest(), m.Manifest()}, manifest.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"windows", "darwin"} {
		srcs, skipped, err := collect.Sources(d, []module.Module{m}, nil, goos)
		if err != nil || len(srcs) != 0 || len(skipped) != 1 || skipped[0].Module != "ups" || skipped[0].Key != "" {
			t.Fatalf("%s: sources %v skipped %+v err %v", goos, srcs, skipped, err)
		}
	}
	if _, ok := d.EntityTemplate("ups", "ups"); !ok || len(d.Attributes) != 23 {
		t.Fatalf("schema must be declared everywhere: %d attributes", len(d.Attributes))
	}
	if srcs, _, _ := collect.Sources(d, []module.Module{m}, nil, "linux"); len(srcs) != 1 || len(srcs[0].Attrs) != 21 {
		t.Fatalf("linux: %+v", srcs)
	}
}
