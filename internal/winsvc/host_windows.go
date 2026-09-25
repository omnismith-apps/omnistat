//go:build windows

package winsvc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// Security descriptors (SDDL). Owner BA so a previous non-admin owner of the
// config directory keeps no implicit right to change it.
const (
	// Config directory: SYSTEM and Administrators full control, Authenticated
	// Users (the service's virtual account among them) read and execute (FR-012).
	configDirSDDL = "O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;AU)"
	// Service key: SYSTEM and Administrators only; the service manager runs as
	// SYSTEM and nothing else needs to read the stored settings (FR-015).
	serviceKeySDDL = "D:P(A;CI;KA;;;SY)(A;CI;KA;;;BA)"

	serviceKeyPath  = `SYSTEM\CurrentControlSet\Services\` + Name
	eventSourcePath = `SYSTEM\CurrentControlSet\Services\EventLog\Application\` + EventSource

	stopTimeout = 2 * time.Minute // final publish is bounded by http.timeout, far less
)

type windowsHost struct{}

// NewHost returns the Host backed by the Windows service manager.
func NewHost() (Host, error) { return windowsHost{}, nil }

func (windowsHost) Elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

func (windowsHost) Executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

func (windowsHost) ProgramDir() (string, error) {
	d, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	return filepath.Join(d, Name), err
}

func (windowsHost) ConfigDir() (string, error) { return configDir() }

func configDir() (string, error) {
	d, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	return filepath.Join(d, Name), err
}

// withService opens the service manager and, if present, the omnistat service.
func withService(fn func(m *mgr.Mgr, s *mgr.Service) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connecting to the service manager: %w", err)
	}
	defer m.Disconnect() //nolint:errcheck // nothing to do about it
	s, err := m.OpenService(Name)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return fn(m, nil)
	}
	if err != nil {
		return err
	}
	defer s.Close()
	return fn(m, s)
}

func (windowsHost) Service(context.Context) (Installed, error) {
	var inst Installed
	err := withService(func(_ *mgr.Mgr, s *mgr.Service) error {
		if s == nil {
			return nil
		}
		cfg, err := s.Config()
		if err != nil {
			return err
		}
		st, err := s.Query()
		if err != nil {
			return err
		}
		env, err := readEnv()
		if err != nil {
			return err
		}
		inst = Installed{Exists: true, Binary: exeOf(cfg.BinaryPathName), State: mapState(st.State), Env: env}
		return nil
	})
	return inst, err
}

// exeOf extracts the executable from a service command line.
func exeOf(cmdline string) string {
	cmdline = strings.TrimSpace(cmdline)
	if strings.HasPrefix(cmdline, `"`) {
		if end := strings.Index(cmdline[1:], `"`); end >= 0 {
			return cmdline[1 : end+1]
		}
	}
	if i := strings.Index(strings.ToLower(cmdline), ".exe"); i >= 0 {
		return cmdline[:i+len(".exe")]
	}
	return cmdline
}

func mapState(s svc.State) State {
	switch s {
	case svc.Stopped:
		return StateStopped
	case svc.StartPending:
		return StateStartPending
	case svc.Running:
		return StateRunning
	case svc.StopPending:
		return StateStopPending
	}
	return StateOther
}

func readEnv() (map[string]string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, serviceKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	lines, _, err := k.GetStringsValue("Environment")
	if errors.Is(err, registry.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(lines))
	for _, l := range lines {
		if k, v, ok := strings.Cut(l, "="); ok && k != "" {
			env[k] = v
		}
	}
	return env, nil
}

func (windowsHost) CopyBinary(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // mode bits are ignored on Windows; Program Files ACLs are inherited (FR-007)
		return err
	}
	in, err := os.Open(src) //nolint:gosec // the running binary
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // program directory
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst) // MoveFileEx with REPLACE_EXISTING
}

func (windowsHost) RemoveBinary(path string) (bool, error) {
	err := os.Remove(path)
	switch {
	case err == nil, errors.Is(err, os.ErrNotExist):
		_ = os.Remove(filepath.Dir(path)) // only if empty
		return false, nil
	}
	// In use (it is the running program): delete at the next restart, the
	// file first, then its directory (FR-020).
	for _, p := range []string{path, filepath.Dir(path)} {
		p16, perr := windows.UTF16PtrFromString(p)
		if perr != nil {
			return false, err
		}
		if merr := windows.MoveFileEx(p16, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT); merr != nil {
			return false, fmt.Errorf("%w (and scheduling removal at restart failed: %w)", err, merr)
		}
	}
	return true, nil
}

func (windowsHost) EnsureConfigDir(path string) (bool, error) {
	_, statErr := os.Stat(path)
	existed := statErr == nil
	if err := os.MkdirAll(path, 0o755); err != nil { //nolint:gosec // mode bits are ignored on Windows; the DACL below decides (FR-012)
		return false, err
	}
	before := sddlOf(path, windows.SE_FILE_OBJECT)
	targets := []string{path}
	if _, err := os.Stat(filepath.Join(path, ConfigFile)); err == nil {
		targets = append(targets, filepath.Join(path, ConfigFile))
	}
	for _, p := range targets {
		if err := applySDDL(p, windows.SE_FILE_OBJECT, configDirSDDL); err != nil {
			return false, fmt.Errorf("restricting %s: %w", p, err)
		}
	}
	return existed && before != sddlOf(path, windows.SE_FILE_OBJECT), nil
}

func sddlOf(object string, typ windows.SE_OBJECT_TYPE) string {
	sd, err := windows.GetNamedSecurityInfo(object, typ, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return ""
	}
	return sd.String()
}

// applySDDL sets the owner (when given) and a protected DACL on object.
func applySDDL(object string, typ windows.SE_OBJECT_TYPE, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	owner, _, err := sd.Owner()
	if err == nil && owner != nil {
		info |= windows.OWNER_SECURITY_INFORMATION
	} else {
		owner = nil
	}
	return windows.SetNamedSecurityInfo(object, typ, info, owner, nil, dacl, nil)
}

func (windowsHost) Register(_ context.Context, r Registration) error {
	return withService(func(m *mgr.Mgr, s *mgr.Service) error {
		cfg := mgr.Config{
			StartType:        mgr.StartAutomatic,
			DelayedAutoStart: true,
			ErrorControl:     mgr.ErrorNormal,
			DisplayName:      r.DisplayName,
			Description:      r.Description,
			ServiceStartName: r.Account,
			SidType:          windows.SERVICE_SID_TYPE_UNRESTRICTED,
		}
		if s == nil {
			created, err := m.CreateService(Name, r.Binary, cfg, r.Args...)
			if err != nil {
				return explainSCM(err)
			}
			defer created.Close()
			s = created
		} else {
			cur, err := s.Config()
			if err != nil {
				return err
			}
			cur.BinaryPathName = commandLine(r.Binary, r.Args)
			cur.StartType, cur.DelayedAutoStart, cur.ErrorControl = cfg.StartType, cfg.DelayedAutoStart, cfg.ErrorControl
			cur.DisplayName, cur.Description = cfg.DisplayName, cfg.Description
			cur.ServiceStartName, cur.SidType = cfg.ServiceStartName, cfg.SidType
			if err := s.UpdateConfig(cur); err != nil {
				return explainSCM(err)
			}
		}
		restart := mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: r.RestartDelay}
		if err := s.SetRecoveryActions([]mgr.RecoveryAction{restart, restart, restart}, uint32((24 * time.Hour).Seconds())); err != nil {
			return fmt.Errorf("recovery actions: %w", err)
		}
		// Without this, a clean exit with a non-zero code (FR-023) is not a
		// failure to Windows and is never restarted (FR-010).
		if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
			return fmt.Errorf("recovery on non-crash failures: %w", err)
		}
		return nil
	})
}

// commandLine mirrors how mgr.CreateService builds the service command line.
func commandLine(exe string, args []string) string {
	s := syscall.EscapeArg(exe)
	for _, a := range args {
		s += " " + syscall.EscapeArg(a)
	}
	return s
}

func (windowsHost) StoreEnv(_ context.Context, env map[string]string) error {
	// Restrict the key first, so the settings are never readable by others,
	// not even for a moment (FR-015).
	if err := applySDDL(`MACHINE\`+serviceKeyPath, windows.SE_REGISTRY_KEY, serviceKeySDDL); err != nil {
		return fmt.Errorf("restricting the service key: %w", err)
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, serviceKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	lines := make([]string, 0, len(env))
	for _, name := range slices.Sorted(maps.Keys(env)) {
		lines = append(lines, name+"="+env[name])
	}
	return k.SetStringsValue("Environment", lines)
}

func (windowsHost) EventSource(install bool) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, eventSourcePath, registry.QUERY_VALUE)
	exists := err == nil
	if exists {
		_ = k.Close()
	}
	switch {
	case install && !exists:
		return eventlog.InstallAsEventCreate(EventSource, eventlog.Error|eventlog.Warning|eventlog.Info)
	case !install && exists:
		return eventlog.Remove(EventSource)
	}
	return nil
}

func (windowsHost) Start(context.Context) error {
	return withService(func(_ *mgr.Mgr, s *mgr.Service) error {
		if s == nil {
			return errors.New("service is not installed")
		}
		if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return err
		}
		return nil
	})
}

func (windowsHost) Stop(ctx context.Context) error {
	return withService(func(_ *mgr.Mgr, s *mgr.Service) error {
		if s == nil {
			return nil
		}
		if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
		deadline := time.Now().Add(stopTimeout)
		for {
			st, err := s.Query()
			if err != nil {
				return err
			}
			if st.State == svc.Stopped {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("the service did not stop within %s", stopTimeout)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
	})
}

func (windowsHost) State(context.Context) (State, error) {
	var st State
	err := withService(func(_ *mgr.Mgr, s *mgr.Service) error {
		if s == nil {
			st = StateStopped
			return nil
		}
		q, err := s.Query()
		st = mapState(q.State)
		return err
	})
	return st, err
}

func (windowsHost) Delete(context.Context) error {
	return withService(func(_ *mgr.Mgr, s *mgr.Service) error {
		if s == nil {
			return nil
		}
		return explainSCM(s.Delete())
	})
}

// explainSCM adds the operator's way out to the errors that need one.
func explainSCM(err error) error {
	if errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return fmt.Errorf("%w: close the Services console (services.msc) and any other tool holding the service, or restart Windows, then run the command again", err)
	}
	return err
}

func (windowsHost) PromptSecret(prompt string) (string, error) {
	in := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(in, &mode); err != nil {
		return "", ErrNotInteractive
	}
	fmt.Fprint(os.Stderr, prompt)
	if err := windows.SetConsoleMode(in, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return "", err
	}
	defer windows.SetConsoleMode(in, mode) //nolint:errcheck // best effort restore
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprintln(os.Stderr)
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
