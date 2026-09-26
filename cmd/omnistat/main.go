// Command omnistat is a modular Omnismith exporter: each module declares the
// templates and attributes it needs, the core reconciles that schema in the
// target project, then publishes the module's dimensions and ingests its metrics.
//
// Behaviour is defined by the specifications under specs/.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/cpu"
	"github.com/omnismith-apps/omnistat/internal/module/disk"
	"github.com/omnismith-apps/omnistat/internal/module/hostname"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/module/memory"
	"github.com/omnismith-apps/omnistat/internal/systemd"
	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Started by the Windows service manager: stop requests instead of signals,
	// the Application log instead of stderr, the service's config file (spec 006).
	if winsvc.IsService() {
		os.Exit(winsvc.Run(func(ctx context.Context, events winsvc.Sink, configPath string) int {
			app := &cli.App{Registry: registry(), Version: resolveVersion(), DefaultConfig: configPath, Events: events}
			return app.Run(ctx, os.Args[1:], winsvc.LineWriter(events.Info), winsvc.LineWriter(events.Error), os.Getenv)
		}))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// The first signal cancels ctx (the daemon then flushes and exits, spec 003
	// FR-020); releasing the handler right away lets a second signal terminate
	// the process through the default action.
	go func() {
		<-ctx.Done()
		stop()
	}()
	app := &cli.App{Registry: registry(), Version: resolveVersion()}
	// Under systemd, stderr is the journal: records and errors carry their
	// priority (spec 007 FR-022).
	var stderr io.Writer = os.Stderr
	if systemd.JournalStream(os.Stderr, os.Getenv) {
		app.Journal = os.Stderr
		stderr = systemd.PriorityWriter(os.Stderr, systemd.PrioErr)
	}
	code := app.Run(ctx, os.Args[1:], os.Stdout, stderr, os.Getenv)
	stop()
	os.Exit(code)
}

// registry lists the modules compiled into this build.
func registry() *module.Registry {
	r := module.NewRegistry()
	r.Register(machineid.New(), module.Required()) // spec 002 FR-002
	r.Register(hostname.New())                     // spec 003 FR-023
	r.Register(cpu.New())                          // spec 004 FR-002
	r.Register(memory.New())                       // spec 005 FR-002
	r.Register(disk.New())                         // spec 008 FR-002
	return r
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
