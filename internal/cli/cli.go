// Package cli implements the omnistat command line: argument parsing,
// logging setup, exit codes and the wiring of config → modules → manifests →
// Omnismith client → schema reconciliation (spec 001 FR-020/021/028).
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/systemd"
	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// Exit codes. Plan uses ExitChanges to tell scripts changes are pending
// (spec 001 FR-021); run uses ExitPartial — the same "attention needed, not
// an error" status — when some module failed to collect (spec 003 FR-018).
const (
	ExitOK      = 0
	ExitError   = 1
	ExitChanges = 2
	ExitPartial = 2
)

// App is the CLI bound to a module registry and a version string. Clock
// drives the run loop (spec 003); nil means the wall clock. MaxPerMetric
// overrides the metric buffer bound; 0 means the spec's 5 000 (FR-008).
// GOOS decides which attributes are collectable here (spec 004 FR-019);
// empty means the platform this binary runs on. All three exist for tests.
//
// DefaultConfig is the config file used when --config is absent, and only if it
// exists; empty means ./omnistat.yaml. Events, when set, receives every log
// record instead of stderr. The Windows service sets both (spec 006 FR-012,
// FR-025). Journal, when set, is the journal stream that receives every log
// record with its priority (spec 007 FR-022). ServiceHost and SystemdHost are
// the Windows service manager and systemd seams of `omnistat service`
// (006 NFR-003, 007 NFR-004), set by tests; with neither, the platform's own
// is used. ServiceSettle shortens install's post-start watch in tests; 0
// means 5s (FR-011).
type App struct {
	Registry      *module.Registry
	Version       string
	Clock         collect.Clock
	MaxPerMetric  int
	GOOS          string
	DefaultConfig string
	Events        winsvc.Sink
	Journal       io.Writer
	ServiceHost   winsvc.Host
	SystemdHost   systemd.Host
	ServiceSettle time.Duration
}

// goos is the platform the collectable check gates on (spec 004 FR-017…022).
func (a *App) goos() string {
	if a.GOOS != "" {
		return a.GOOS
	}
	return runtime.GOOS
}

// env is everything a command needs from the outside world.
type env struct {
	ctx    context.Context
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string
	log    *slog.Logger
	config string // --config path
	// defaultConfig is probed when config is empty (spec 006 FR-012).
	defaultConfig string
}

// loadConfig loads the settings every command runs with.
func (e env) loadConfig() (config.Settings, error) {
	return config.LoadWithDefault(e.config, e.defaultConfig, e.getenv)
}

// Run executes args (without the program name) and returns the exit code.
func (a *App) Run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("omnistat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "path to omnistat.yaml (default: ./omnistat.yaml if present)")
	logLevel := fs.String("log-level", "", "debug|info|warn|error (default from config, else info)")
	logFormat := fs.String("log-format", "", "text|json (default from config, else text)")
	fs.Usage = func() { a.usage(stderr, fs) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitError
	}
	rest := fs.Args()
	if len(rest) == 0 {
		a.usage(stderr, fs)
		return ExitError
	}

	// Logging is configured from flags first so config errors are logged too;
	// config values fill in what flags left empty.
	defaultConfig := config.DefaultPath
	if a.DefaultConfig != "" {
		defaultConfig = a.DefaultConfig
	}
	level, format := *logLevel, *logFormat
	if level == "" || format == "" {
		if s, err := config.LoadWithDefault(*cfgPath, defaultConfig, getenv); err == nil {
			if level == "" {
				level = s.Log.Level
			}
			if format == "" {
				format = s.Log.Format
			}
		}
	}
	e := env{ctx: ctx, stdout: stdout, stderr: stderr, getenv: getenv, log: a.newLogger(stderr, level, format), config: *cfgPath, defaultConfig: defaultConfig}
	// Modules log through slog's default (omission records, notices, debug
	// records). Make it the process logger for this run so that they honour
	// log.level and log.format and reach the journal or the Event Log
	// (003 FR-026, amended 2026-09-26).
	prevLog := slog.Default()
	slog.SetDefault(e.log)
	defer slog.SetDefault(prevLog)

	switch rest[0] {
	case "version":
		fmt.Fprintf(stdout, "omnistat %s\n", a.Version)
		return ExitOK
	case "schema":
		return a.schema(e, rest[1:])
	case "identity":
		return a.identity(e, rest[1:])
	case "run":
		return a.run(e, rest[1:])
	case "service":
		return a.service(e, rest[1:])
	case "help", "-h", "--help":
		a.usage(stdout, fs)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "omnistat: unknown command %q\n\n", rest[0])
		a.usage(stderr, fs)
		return ExitError
	}
}

func (a *App) usage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, `omnistat — modular Omnismith exporter and schema scaffolder

Usage:
  omnistat [global flags] <command> [args]

Commands:
  schema plan [--json]   show what reconciliation would create (exit 0 none, 2 pending, 1 conflict/error)
  schema apply           create what is missing (additive only)
  schema verify          fail if anything is missing; write nothing
  identity [--json]      show this host's identity, its source and the entity it resolves to; write nothing
  run [--daemon] [--dry-run [--json]]
                         reconcile, resolve the host entity, collect every module and publish once
                         (exit 0 ok, 2 some module failed, 1 error); --daemon keeps collecting and
                         publishes every publish.interval; --dry-run prints what would be sent
  service install [--dry-run] [--replace-token]
                         install (or update) omnistat as a service that runs from boot: a Windows
                         service, or a systemd unit on Linux; needs an elevated prompt (Windows)
                         or root (sudo); the token is read from OMNISMITH_ACCESS_TOKEN or asked
                         for without echo, a missing project id is asked for
  service uninstall [--dry-run]
                         remove the service, its stored settings and the installed binary;
                         the configuration is kept
  version                print the version
  help                   this text

Global flags:
`)
	prev := fs.Output()
	fs.SetOutput(w)
	fs.PrintDefaults()
	fs.SetOutput(prev)
	fmt.Fprintf(w, "\nModules in this build: %s\n", strings.Join(a.Registry.Names(), ", "))
	fmt.Fprintf(w, "Environment: %s (required), %s, %s, %s\n", config.EnvToken, config.EnvProjectID, config.EnvBaseURL, config.EnvIdentity)
}

// fail prints an error for humans and returns ExitError.
func fail(e env, err error) int {
	fmt.Fprintf(e.stderr, "omnistat: %v\n", err)
	return ExitError
}
