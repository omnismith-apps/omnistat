// Command e2e-mirror serves a directory over HTTP on a loopback address: the
// releases mirror of the container acceptance of `omnistat upgrade`
// (spec 009 NFR-006, scripts/e2e-upgrade.sh). It is test tooling, not shipped.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "listen address (keep it loopback: upgrade accepts plain HTTP only there)")
	dir := flag.String("dir", ".", "directory with the releases layout: latest/download/…, download/<tag>/…")
	flag.Parse()
	srv := &http.Server{Addr: *addr, Handler: http.FileServer(http.Dir(*dir)), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
