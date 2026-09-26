package upgrade

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// versionTimeout bounds `omnistat version`, which returns at once.
const versionTimeout = 30 * time.Second

// Runner runs omnistat binaries: the installed one and the download
// (FR-005, FR-009, FR-013). Tests replace it.
type Runner interface {
	// Version returns what `<bin> version` prints.
	Version(ctx context.Context, bin string) (string, error)
	// Install runs `<bin> args…` attached to the terminal, with upgrade's
	// environment, and returns its exit status.
	Install(bin string, args []string, stdout, stderr io.Writer) (int, error)
}

// ExecRunner runs the binaries as child processes.
type ExecRunner struct{}

// Version runs `<bin> version` with a deadline.
func (ExecRunner) Version(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	return string(out), err
}

// Install runs the child without a context on purpose: an interrupt reaches
// it from the terminal and it handles it itself; upgrade never kills an
// install halfway (FR-013). Its environment is upgrade's, unchanged.
func (ExecRunner) Install(bin string, args []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(bin, args...) //nolint:gosec,noctx // the verified binary; see above
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
