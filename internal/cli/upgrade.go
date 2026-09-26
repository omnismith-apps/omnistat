package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/upgrade"
)

// upgrade implements `omnistat upgrade [--check | --dry-run] [--version v]`
// (spec 009): find the release, verify it, and hand over to its own
// `service install`.
func (a *App) upgrade(e env, args []string) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	check := fs.Bool("check", false, "only report whether an upgrade is available (exit 2 if so); change nothing")
	dryRun := fs.Bool("dry-run", false, "download and verify, then show what the new version's install would change; change nothing")
	want := fs.String("version", "", "install exactly this release (vX.Y.Z, pre-releases included; older ones too)")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	if fs.NArg() > 0 || (*check && *dryRun) {
		fmt.Fprintln(e.stderr, "usage: omnistat upgrade [--check | --dry-run] [--version vX.Y.Z]")
		return ExitError
	}

	inst, err := a.installer()
	if errors.Is(err, errServiceUnsupported) {
		// FR-004
		fmt.Fprintf(e.stderr, "omnistat: upgrade: not supported on %s — upgrade updates the service installed by `omnistat service install` (Windows, and Linux with systemd); elsewhere download the new release from %s\n", a.goos(), upgrade.DefaultReleasesURL)
		return ExitError
	}
	if err != nil {
		return fail(e, err)
	}
	// FR-002, FR-003: rights and the installed service, before any network.
	in, err := inst.Installation(e.ctx)
	if err != nil {
		return fail(e, err)
	}
	if !in.Exists {
		fmt.Fprintf(e.stderr, "omnistat: upgrade: omnistat is not installed as a service on this host — upgrade updates the installed service; install it with `omnistat service install`, or download the new release from %s\n", upgrade.DefaultReleasesURL)
		return ExitError
	}
	base, err := upgrade.ReleasesURL(e.getenv)
	if err != nil {
		return fail(e, err)
	}
	client := upgrade.NewClient(upgrade.ProxySettings(e.getenv, in.Settings)) // FR-012
	runner := a.Runner
	if runner == nil {
		runner = upgrade.ExecRunner{}
	}

	title := "omnistat upgrade"
	switch {
	case *check:
		title += " --check — nothing is changed"
	case *dryRun:
		title += " — dry run, nothing is changed"
	}
	fmt.Fprintln(e.stdout, title)

	// FR-005: the installed service's version, not this binary's.
	out, verr := runner.Version(e.ctx, in.Binary)
	installed, known := upgrade.ParseVersionOutput(out)
	switch {
	case known:
		fmt.Fprintf(e.stdout, "  installed: %s (%s)\n", installed, in.Binary)
	case verr != nil:
		fmt.Fprintf(e.stdout, "  installed: unknown — %s did not report its version (%v); any release counts as newer\n", in.Binary, verr)
	default:
		fmt.Fprintf(e.stdout, "  installed: unknown — %s reports %q, not a release version; any release counts as newer\n", in.Binary, strings.TrimSpace(out))
	}

	rel, err := upgrade.Resolve(e.ctx, client, base, *want, a.goos(), runtime.GOARCH)
	if err != nil {
		return fail(e, err)
	}
	which := "the latest release"
	if *want != "" {
		which = "--version"
	}
	fmt.Fprintf(e.stdout, "  target:    %s (%s)\n", rel.Version, which)

	// FR-007, FR-017
	cmp := 1
	if known {
		cmp = upgrade.Compare(rel.Version, installed)
	}
	switch {
	case cmp == 0:
		fmt.Fprintf(e.stdout, "omnistat is up to date (%s); nothing to do.\n", installed)
		return ExitOK
	case cmp < 0 && *want == "":
		fmt.Fprintf(e.stdout, "The installed %s is newer than the latest release %s; nothing to do. To go back, name the release with --version.\n", installed, rel.Version)
		return ExitOK
	case *check:
		fmt.Fprintf(e.stdout, "An upgrade is available: run `omnistat upgrade%s` with the same rights to install %s.\n", pinFlag(*want), rel.Version)
		return ExitChanges
	case cmp < 0:
		fmt.Fprintf(e.stdout, "  note:      %s is older than the installed %s: this is a downgrade\n", rel.Version, installed)
	}

	// FR-009, FR-011: nothing about the service changes before this passes.
	dir := a.StageDir
	if dir == "" {
		dir = filepath.Dir(in.Binary)
	}
	stage, cleanup, err := upgrade.Stage(dir)
	if err != nil {
		return fail(e, fmt.Errorf("preparing a directory for the download in %s: %w", dir, err))
	}
	defer cleanup()
	fmt.Fprintf(e.stdout, "  download:  %s\n", rel.ArchiveURL)
	bin, err := upgrade.Fetch(e.ctx, client, rel, stage, a.goos())
	if err != nil {
		return fail(e, fmt.Errorf("%w; nothing was changed", err))
	}
	out, err = runner.Version(e.ctx, bin)
	if got, ok := upgrade.ParseVersionOutput(out); err != nil || !ok || upgrade.Compare(got, rel.Version) != 0 {
		if err != nil {
			return fail(e, fmt.Errorf("the downloaded binary does not run on this host: %w; nothing was changed", err))
		}
		return fail(e, fmt.Errorf("the downloaded binary reports %q, not %s; nothing was changed", strings.TrimSpace(out), rel.Version))
	}
	fmt.Fprintf(e.stdout, "  verified:  SHA-256 matches %s; the binary reports %s\n", rel.ChecksumsURL, rel.Version)

	// FR-013: the new version installs itself.
	iargs := []string{"service", "install"}
	if *dryRun {
		iargs = append(iargs, "--dry-run")
	}
	// Windows cannot replace the program upgrade runs from: step aside.
	var aside *upgrade.Aside
	if self, err := os.Executable(); err == nil && !*dryRun && upgrade.RunsFrom(self, in.Binary, a.goos()) {
		if aside, err = upgrade.StepAside(self, stage); err != nil {
			return fail(e, fmt.Errorf("moving the running binary out of the way: %w; nothing was changed", err))
		}
		fmt.Fprintf(e.stdout, "  note:      this upgrade runs from %s; it moved to %s to make way (removed at the next restart)\n", in.Binary, aside.Moved())
	}
	fmt.Fprintf(e.stdout, "Handing over to omnistat %s: %s\n\n", rel.Version, strings.Join(iargs, " "))
	code, err := runner.Install(bin, iargs, e.stdout, e.stderr)
	if aside != nil {
		restored, ferr := aside.Finish()
		switch {
		case restored && ferr != nil:
			fmt.Fprintf(e.stderr, "omnistat: the install left no binary at %s, and putting the previous one back failed: %v\n", in.Binary, ferr)
		case restored:
			fmt.Fprintf(e.stderr, "omnistat: the install left no binary at %s; the previous one is back\n", in.Binary)
		case ferr != nil:
			fmt.Fprintf(e.stderr, "omnistat: %v\n", ferr)
		}
	}
	if err != nil {
		return fail(e, fmt.Errorf("running the downloaded binary: %w; nothing was changed", err))
	}
	// FR-014
	switch {
	case code != 0:
		fmt.Fprintf(e.stderr, "omnistat: upgrade to %s did not complete: `%s` exited with status %d; its output above names the step that failed\n", rel.Version, strings.Join(iargs, " "), code)
		return code
	case *dryRun:
		fmt.Fprintf(e.stdout, "\nDry run: nothing was changed, and the download is removed.\n")
	case known:
		fmt.Fprintf(e.stdout, "\nomnistat is upgraded from %s to %s.\n", installed, rel.Version)
	default:
		fmt.Fprintf(e.stdout, "\nomnistat is upgraded to %s.\n", rel.Version)
	}
	return ExitOK
}

func pinFlag(want string) string {
	if want == "" {
		return ""
	}
	return " --version " + want
}
