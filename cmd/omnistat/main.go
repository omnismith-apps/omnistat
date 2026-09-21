// Command omnistat is a modular Omnismith exporter: each module declares the
// templates and attributes it needs, the core reconciles that schema in the
// target project, then publishes the module's dimensions and ingests its metrics.
//
// Behaviour is defined by the specifications under specs/. Nothing beyond
// this entry point exists until a spec has been agreed and planned.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("omnistat", resolveVersion())
		return
	}
	fmt.Fprintln(os.Stderr, "omnistat: no behaviour implemented yet — see specs/README.md")
	os.Exit(2)
}

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}
