//go:build linux

package systemd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/omnismith-apps/omnistat/internal/service"
)

// NewHost returns the real systemd seam.
func NewHost() (Host, error) { return linuxHost{}, nil }

type linuxHost struct{}

func (linuxHost) Root() bool { return os.Geteuid() == 0 }

// Booted is sd_booted(3): systemd creates /run/systemd/system at boot.
func (linuxHost) Booted() bool {
	fi, err := os.Lstat("/run/systemd/system")
	return err == nil && fi.IsDir()
}

func (linuxHost) Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func (linuxHost) Stat(path string) (File, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, err
	}
	f := File{Exists: true, Dir: fi.IsDir(), Mode: fi.Mode().Perm(), UID: -1}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		f.UID = int(st.Uid)
	}
	return f, nil
}

func (linuxHost) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) } //nolint:gosec // fixed paths of the service

// WriteFile creates the temporary file in path's own directory, so the result
// gets that directory's default SELinux label rather than the label of
// wherever the data came from.
func (linuxHost) WriteFile(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // gone after the rename; cleanup on failure
	if err := own(tmp, mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (linuxHost) CreateFile(path string, data []byte, mode fs.FileMode) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // fixed paths of the service
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := own(f, mode); err != nil {
		_ = f.Close()
		return true, err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return true, err
	}
	return true, f.Close()
}

// own sets mode exactly (the umask applied at creation) and, as root, makes
// root the owner.
func own(f *os.File, mode fs.FileMode) error {
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return f.Chown(0, 0)
	}
	return nil
}

func (linuxHost) MkdirAll(path string, mode fs.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return os.Chown(path, 0, 0)
	}
	return nil
}

func (linuxHost) Secure(path string, mode fs.FileMode) error {
	if err := os.Chown(path, 0, 0); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func (linuxHost) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (linuxHost) Unit(ctx context.Context) (Unit, error) {
	out, err := exec.CommandContext(ctx, "systemctl", "show", UnitName, "--no-pager",
		"-p", "LoadState", "-p", "FragmentPath", "-p", "ActiveState", "-p", "SubState", "-p", "DropInPaths").Output()
	if err != nil {
		return Unit{}, fmt.Errorf("systemctl show %s: %w", UnitName, err)
	}
	return ParseShow(string(out)), nil
}

func (linuxHost) Systemctl(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput() //nolint:gosec // fixed verbs and unit name from the plans
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return nil
}

// console reads the operator's answers; one reader for every prompt, so none
// loses input another buffered.
var console = bufio.NewReader(os.Stdin)

func (linuxHost) PromptSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return "", service.ErrNotInteractive
	}
	fmt.Fprint(os.Stderr, prompt)
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &quiet); err != nil {
		return "", err
	}
	defer unix.IoctlSetTermios(fd, unix.TCSETS, old) //nolint:errcheck // best effort restore
	line, err := console.ReadString('\n')
	fmt.Fprintln(os.Stderr)
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (linuxHost) PromptLine(prompt string) (string, error) {
	if _, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS); err != nil {
		return "", service.ErrNotInteractive
	}
	fmt.Fprint(os.Stderr, prompt)
	line, err := console.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
