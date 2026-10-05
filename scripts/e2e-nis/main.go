// Command e2e-nis is a fake apcupsd Network Information Server on a loopback
// address: the UPS of the container acceptance of the `ups` module (spec 012
// NFR-006, scripts/e2e-systemd.sh). It answers every `status` request with a
// fixed USB Back-UPS status, framed as apcupsd 3.14 does. It is test tooling,
// not shipped.
package main

import (
	"encoding/binary"
	"flag"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

const status = `APC      : 001,036,0879
DATE     : 2026-10-05 21:14:07 +0000  
UPSNAME  : e2e-ups
MODEL    : Back-UPS XS 950U
STATUS   : ONLINE 
LINEV    : 232.0 Volts
LOADPCT  : 12.0 Percent
BCHARGE  : 100.0 Percent
TIMELEFT : 48.7 Minutes
BATTV    : 13.6 Volts
LASTXFER : Low line voltage
NUMXFERS : 1
XONBATT  : 2026-10-04 18:02:11 +0000  
SELFTEST : NO
STATFLAG : 0x05000008
SERIALNO : %SERIAL%
BATTDATE : 2023-05-14
NOMPOWER : 480 Watts
END APC  : 2026-10-05 21:14:08 +0000  
`

func main() {
	addr := flag.String("addr", "127.0.0.1:3551", "listen address")
	serial := flag.String("serial", "E2E0000001", "the UPS serial number to report")
	flag.Parse()
	reply := strings.ReplaceAll(status, "%SERIAL%", *serial)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go serve(c, reply)
	}
}

func serve(c net.Conn, reply string) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
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
		_ = binary.Write(c, binary.BigEndian, uint16(len(l))) //nolint:gosec // the lines are short constants
		_, _ = io.WriteString(c, l)
	}
	_, _ = c.Write([]byte{0, 0})
}
