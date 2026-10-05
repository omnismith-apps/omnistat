package cli_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/module/ups"
)

// serveNIS answers every status request with reply, framed as apcupsd does,
// and returns the address.
func serveNIS(t *testing.T, reply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				head := make([]byte, 2)
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, binary.BigEndian.Uint16(head))); err != nil {
					return
				}
				for _, l := range strings.SplitAfter(reply, "\n") {
					if l == "" {
						continue
					}
					_ = binary.Write(c, binary.BigEndian, uint16(len(l))) //nolint:gosec // test lines are short
					_, _ = io.WriteString(c, l)
				}
				_, _ = c.Write([]byte{0, 0})
			}(c)
		}
	}()
	return ln.Addr().String()
}

const nisReply = `STATUS   : ONBATT 
UPSNAME  : garage
MODEL    : Back-UPS XS 950U
LINEV    : 0.0 Volts
LOADPCT  : 20.0 Percent
BCHARGE  : 95.0 Percent
TIMELEFT : 30.5 Minutes
BATTV    : 13.1 Volts
LASTXFER : Low line voltage
XONBATT  : 2026-10-05 21:19:41 +0300  
SELFTEST : NO
STATFLAG : 0x05060010
SERIALNO : 4B1234P56789
NOMPOWER : 480 Watts
`

// spec 012 US-1/1-2, US-2/1, US-5/2-3, FR-005, FR-021 end to end against the
// fakes: the UPS becomes its own entity on template ups, linked to the host,
// with its state and readings; the schedule log names the address.
func TestRun_UPS(t *testing.T) {
	addr := serveNIS(t, nisReply)
	h := newHarness(t, "modules:\n  ups:\n    enabled: true\n    address: "+addr+"\n")
	h.reg.Register(ups.New(), module.DisabledByDefault())
	r := h.exec(context.Background(), "run")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	host := h.byKey("host", machineid.Derive(rawID))
	u := h.byKey("ups", "4B1234P56789")
	if host == nil || u == nil {
		t.Fatalf("entities: %+v", h.srv.Entities())
	}
	if u.Values["ups_host"] != host.ID || u.Values["ups_on_battery"] != true || u.Values["ups_comm_lost"] != false ||
		u.Values["ups_name"] != "garage" || u.Values["ups_last_transfer_reason"] != "Low line voltage" || u.Values["ups_last_transfer_at"] != "2026-10-05T18:19:41Z" {
		t.Fatalf("UPS values: %v", u.Values)
	}
	m := h.srv.EntityMetrics(u.ID)
	if len(m["ups_battery_charge_pct"]) != 1 || m["ups_battery_charge_pct"][0].Value != "95" || m["ups_load_w"][0].Value != "96" {
		t.Fatalf("UPS metrics: %v", m)
	}
	if !strings.Contains(h.logsSnapshot(), `msg="module scheduled" module=ups interval=10s address=`+addr) {
		t.Fatalf("schedule log:\n%s", h.logsSnapshot())
	}
}

// spec 012 FR-005: an unknown ups setting fails before any network call.
func TestRun_UPSUnknownSetting(t *testing.T) {
	h := newHarness(t, "modules:\n  ups:\n    addr: 127.0.0.1:3551\n")
	h.reg.Register(ups.New(), module.DisabledByDefault())
	r := h.exec(context.Background(), "schema", "plan")
	if r.code != 1 || !strings.Contains(r.stderr, "modules.ups.addr: unknown setting") {
		t.Fatalf("%+v", r)
	}
	if len(h.srv.Requests()) != 0 {
		t.Fatal("network used before the config was checked")
	}
}
