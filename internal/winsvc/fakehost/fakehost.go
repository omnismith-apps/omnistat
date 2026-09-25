// Package fakehost is an in-memory winsvc.Host for tests (spec 006 NFR-003).
// It records every mutating call in order, so tests can assert the sequence
// install and uninstall apply, and that a dry-run applies nothing.
package fakehost

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/omnismith-apps/omnistat/internal/winsvc"
)

// Host is a scriptable winsvc.Host. Set the exported fields before use.
type Host struct {
	IsElevated bool
	Exe        string
	ProgDir    string
	CfgDir     string

	// Svc is the omnistat service as the service manager knows it.
	Svc winsvc.Installed
	// StateAfterStart is what the service settles into after Start
	// (zero value: stopped, so tests of success set it to StateRunning).
	StateAfterStart winsvc.State
	// Secret answers PromptSecret; SecretErr fails it (e.g. ErrNotInteractive).
	Secret    string
	SecretErr error
	// InUse lists binaries that cannot be deleted now (the running program).
	InUse map[string]bool
	// LooseConfigDir makes EnsureConfigDir report that it tightened permissions.
	LooseConfigDir bool
	// Fail makes the named method return the error.
	Fail map[string]error

	mu          sync.Mutex
	calls       []string
	prompts     int
	registered  *winsvc.Registration
	eventSource bool
}

var _ winsvc.Host = (*Host)(nil)

// Calls returns the mutating calls made so far, in order.
func (h *Host) Calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

// Prompts returns how many times PromptSecret was called.
func (h *Host) Prompts() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.prompts
}

// Registered returns the last registration written, or nil.
func (h *Host) Registered() *winsvc.Registration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.registered
}

// EventSourceRegistered reports whether the event source is registered.
func (h *Host) EventSourceRegistered() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.eventSource
}

func (h *Host) record(format string, args ...any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	call := fmt.Sprintf(format, args...)
	h.calls = append(h.calls, call)
	name, _, _ := strings.Cut(call, " ")
	return h.Fail[name]
}

// Elevated implements winsvc.Host.
func (h *Host) Elevated() bool { return h.IsElevated }

// Executable implements winsvc.Host.
func (h *Host) Executable() (string, error) { return h.Exe, nil }

// ProgramDir implements winsvc.Host.
func (h *Host) ProgramDir() (string, error) { return h.ProgDir, nil }

// ConfigDir implements winsvc.Host.
func (h *Host) ConfigDir() (string, error) { return h.CfgDir, nil }

// Service implements winsvc.Host.
func (h *Host) Service(context.Context) (winsvc.Installed, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.Svc
	s.Env = maps.Clone(h.Svc.Env)
	return s, h.Fail["Service"]
}

// CopyBinary implements winsvc.Host.
func (h *Host) CopyBinary(src, dst string) error { return h.record("CopyBinary %s -> %s", src, dst) }

// RemoveBinary implements winsvc.Host.
func (h *Host) RemoveBinary(path string) (bool, error) {
	err := h.record("RemoveBinary %s", path)
	return h.InUse[path], err
}

// EnsureConfigDir implements winsvc.Host.
func (h *Host) EnsureConfigDir(path string) (bool, error) {
	err := h.record("EnsureConfigDir %s", path)
	return h.LooseConfigDir, err
}

// Register implements winsvc.Host.
func (h *Host) Register(_ context.Context, r winsvc.Registration) error {
	err := h.record("Register %s", r.Binary)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.registered = &r
		h.Svc.Exists, h.Svc.Binary = true, r.Binary
	}
	return err
}

// StoreEnv implements winsvc.Host. The call log carries names only, so a test
// can assert on it without handling secrets.
func (h *Host) StoreEnv(_ context.Context, env map[string]string) error {
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	err := h.record("StoreEnv %d", len(names))
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.Svc.Env = maps.Clone(env)
	}
	return err
}

// EventSource implements winsvc.Host.
func (h *Host) EventSource(install bool) error {
	err := h.record("EventSource %t", install)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.eventSource = install
	}
	return err
}

// Start implements winsvc.Host.
func (h *Host) Start(context.Context) error {
	err := h.record("Start")
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.Svc.State = h.StateAfterStart
	}
	return err
}

// Stop implements winsvc.Host.
func (h *Host) Stop(context.Context) error {
	err := h.record("Stop")
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.Svc.State = winsvc.StateStopped
	}
	return err
}

// State implements winsvc.Host.
func (h *Host) State(context.Context) (winsvc.State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.Svc.State, h.Fail["State"]
}

// Delete implements winsvc.Host.
func (h *Host) Delete(context.Context) error {
	err := h.record("Delete")
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.Svc = winsvc.Installed{}
	}
	return err
}

// PromptSecret implements winsvc.Host.
func (h *Host) PromptSecret(string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prompts++
	return h.Secret, h.SecretErr
}
