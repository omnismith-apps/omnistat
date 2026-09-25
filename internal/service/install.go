package service

import (
	"context"
	"time"
)

// Checked is what the install pre-check found (006 FR-006, 007 FR-005).
type Checked struct {
	Identity    string
	Source      string
	EntityID    string
	WouldCreate bool
	// SchemaPending: the project has no identity attribute yet; the service
	// reconciles the schema when it starts.
	SchemaPending bool
}

// Precheck runs the read-only checks of `omnistat identity` with the settings
// the service will get and the config file it will read.
type Precheck func(ctx context.Context, getenv func(string) string, configPath string) (Checked, error)

// InstallOptions are the platform-neutral inputs of a backend's PlanInstall.
type InstallOptions struct {
	// Getenv is the installer's environment.
	Getenv func(string) string
	// ReplaceToken forces the token prompt (006 FR-018).
	ReplaceToken bool
	// Precheck is run before any change; nil skips it.
	Precheck Precheck
	// Settle is how long the service is watched after it starts; zero checks
	// once. Poll is the interval between checks.
	Settle, Poll time.Duration
}

// Watch checks the service until settle has passed since the start
// (006 FR-011, 007 FR-011). check describes the current state, or returns an
// error when the service has stopped or failed, which ends the watch.
func Watch(ctx context.Context, settle, poll time.Duration, check func(context.Context) (string, error)) (string, error) {
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := time.Now().Add(settle)
	for {
		state, err := check(ctx)
		if err != nil {
			return "", err
		}
		if !time.Now().Before(deadline) {
			return "service is " + state, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}
}
