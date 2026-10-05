package ups

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeNIS is an in-process apcupsd Network Information Server. By default it
// answers a status request with reply, one frame per line, then the empty
// frame. behaviour changes that for failure tests.
type fakeNIS struct {
	ln        net.Listener
	mu        sync.Mutex
	reply     string
	behaviour string // "", "hang", "close-early", "huge-frame", "endless"
	closed    chan struct{}
	requests  []string
}

func newFakeNIS(t *testing.T, reply string) *fakeNIS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeNIS{ln: ln, reply: reply, closed: make(chan struct{}, 16)}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve()
	return f
}

func (f *fakeNIS) addr() string { return f.ln.Addr().String() }

func (f *fakeNIS) set(reply, behaviour string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply, f.behaviour = reply, behaviour
}

func (f *fakeNIS) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeNIS) handle(c net.Conn) {
	defer func() {
		// The client closed its side: a read returns EOF.
		_, _ = io.Copy(io.Discard, c)
		_ = c.Close()
		f.closed <- struct{}{}
	}()
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil {
		return
	}
	cmd := make([]byte, binary.BigEndian.Uint16(head))
	if _, err := io.ReadFull(c, cmd); err != nil {
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, string(cmd))
	reply, behaviour := f.reply, f.behaviour
	f.mu.Unlock()
	switch behaviour {
	case "hang":
		return // say nothing; the deferred copy waits for the client to give up
	case "huge-frame":
		_, _ = c.Write([]byte{0x10, 0x00})
		return
	case "endless":
		for i := 0; i < 10000; i++ {
			if _, err := c.Write(frame("LINEV    : 230.0 Volts\n")); err != nil {
				return
			}
		}
		return
	}
	for _, l := range strings.SplitAfter(reply, "\n") {
		if l == "" {
			continue
		}
		if _, err := c.Write(frame(l)); err != nil {
			return
		}
	}
	if behaviour == "close-early" {
		_ = c.(*net.TCPConn).CloseWrite()
		return
	}
	_, _ = c.Write([]byte{0, 0})
}

// replies recorded from apcupsd 3.14's status writer (src/lib/apcstatus.c):
// a USB Back-UPS on mains, with the two trailing spaces apcupsd leaves after
// dates.
const usbOnline = `APC      : 001,036,0879
DATE     : 2026-10-05 21:14:07 +0300  
HOSTNAME : nas
VERSION  : 3.14.14 (31 May 2016) redhat
UPSNAME  : Back-UPS XS 950U
CABLE    : USB Cable
DRIVER   : USB UPS Driver
UPSMODE  : Stand Alone
STARTTIME: 2026-10-01 09:12:44 +0300  
MODEL    : Back-UPS XS 950U  
STATUS   : ONLINE 
LINEV    : 232.0 Volts
LOADPCT  : 12.0 Percent
BCHARGE  : 100.0 Percent
TIMELEFT : 48.7 Minutes
MBATTCHG : 5 Percent
MINTIMEL : 3 Minutes
MAXTIME  : 0 Seconds
SENSE    : Medium
LOTRANS  : 155.0 Volts
HITRANS  : 280.0 Volts
ALARMDEL : 30 Seconds
BATTV    : 13.6 Volts
LASTXFER : Low line voltage
NUMXFERS : 2
XONBATT  : 2026-10-04 18:02:11 +0300  
TONBATT  : 0 Seconds
CUMONBATT: 41 Seconds
XOFFBATT : 2026-10-04 18:02:31 +0300  
LASTSTEST: 2026-10-02 03:00:12 +0300  
SELFTEST : NO
STATFLAG : 0x05000008
SERIALNO : 4B1234P56789  
BATTDATE : 2023-05-14
NOMINV   : 230 Volts
NOMBATTV : 12.0 Volts
NOMPOWER : 480 Watts
FIRMWARE : 925.T2 .I USB FW:T2
END APC  : 2026-10-05 21:14:08 +0300  
`

// smartOnBattery is a Smart-UPS on battery that reports everything,
// including the UPS's own MM/DD/YY battery date and a passed self-test.
const smartOnBattery = `APC      : 001,050,1201
DATE     : 2026-10-05 21:20:00 +0300  
UPSNAME  : rack-ups
MODEL    : Smart-UPS 1500
STATUS   : ONBATT 
LINEV    : 0.0 Volts
LOADPCT  : 31.5 Percent
BCHARGE  : 87.0 Percent
TIMELEFT : 22.3 Minutes
OUTPUTV  : 230.4 Volts
ITEMP    : 29.2 C
BATTV    : 26.1 Volts
LINEFREQ : 50.0 Hz
LASTXFER : Unacceptable line voltage changes
NUMXFERS : 1
XONBATT  : 2026-10-05 21:19:41 +0300  
TONBATT  : 19 Seconds
SELFTEST : OK
STATFLAG : 0x05060010
SERIALNO : AS1234567890
BATTDATE : 05/14/23
NOMPOWER : 980 Watts
END APC  : 2026-10-05 21:20:00 +0300  
`
