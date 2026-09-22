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

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/module"
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
type App struct {
	Registry     *module.Registry
	Version      string
	Clock        collect.Clock
	MaxPerMetric int
	GOOS         string
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
	level, format := *logLevel, *logFormat
	if level == "" || format == "" {
		if s, err := config.Load(*cfgPath, getenv); err == nil {
			if level == "" {
				level = s.Log.Level
			}
			if format == "" {
				format = s.Log.Format
			}
		}
	}
	e := env{ctx: ctx, stdout: stdout, stderr: stderr, getenv: getenv, log: newLogger(stderr, level, format), config: *cfgPath}

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
