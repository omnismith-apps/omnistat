// Command omnistat is a modular Omnismith exporter: each module declares the
// templates and attributes it needs, the core reconciles that schema in the
// target project, then publishes the module's dimensions and ingests its metrics.
//
// Behaviour is defined by the specifications under specs/.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	app := &cli.App{Registry: registry(), Version: resolveVersion()}
	code := app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}

// registry lists the modules compiled into this build. Feature 002 adds the
// first real module (machine-id); until then the schema is the host template only.
func registry() *module.Registry {
	return module.NewRegistry()
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
