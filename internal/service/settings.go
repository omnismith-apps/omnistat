package service

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/omnismith-apps/omnistat/internal/config"
)

// Errors of the settings prompts.
var (
	ErrNotInteractive = errors.New("no terminal to prompt on")
	ErrNoToken        = errors.New("no access token: set " + config.EnvToken + " or run install from an interactive terminal")
)

// Prompter reads answers from the operator's terminal. Both return
// ErrNotInteractive when there is none.
type Prompter interface {
	// PromptSecret reads a line without echo (006 FR-014, 007 FR-015).
	PromptSecret(prompt string) (string, error)
	// PromptLine reads a line with echo (007 FR-017).
	PromptLine(prompt string) (string, error)
}

// Captured lists the environment variables install stores for the service on
// every platform (006 FR-013, 007 FR-013). Each is treated as a secret:
// printed by name only.
var Captured = []string{
	config.EnvToken, config.EnvProjectID, config.EnvBaseURL, config.EnvIdentity,
	"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY",
}

// SettingsOptions are the inputs of ResolveSettings.
type SettingsOptions struct {
	// Stored are the settings the installed service has now (upgrade).
	Stored map[string]string
	// Getenv is the installer's environment.
	Getenv func(string) string
	// Names are the variables to capture from Getenv.
	Names []string
	// ReplaceToken forces the token prompt.
	ReplaceToken bool
	// ConfigPath is the config file the service reads; a project id there
	// needs no prompt (007 FR-017).
	ConfigPath string
	Prompt     Prompter
}

// ResolveSettings merges the stored settings with those set in the
// installer's environment (a set one replaces, an absent one is kept). It asks
// for a project id that is set nowhere, the config file included (007 FR-017),
// and settles the token: from the environment, else stored, else asked for
// without echo; --replace-token always asks (006 FR-013/014/017/018).
func ResolveSettings(o SettingsOptions) (map[string]string, error) {
	env := maps.Clone(o.Stored)
	if env == nil {
		env = map[string]string{}
	}
	getenv := o.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	for _, k := range o.Names {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			env[k] = v
		}
	}
	if err := askProject(o, env); err != nil {
		return nil, err
	}
	fromEnv := strings.TrimSpace(getenv(config.EnvToken)) != ""
	if !fromEnv && (o.ReplaceToken || env[config.EnvToken] == "") {
		tok, err := o.Prompt.PromptSecret("Omnismith access token (input hidden): ")
		switch {
		case errors.Is(err, ErrNotInteractive):
			return nil, ErrNoToken
		case err != nil:
			return nil, fmt.Errorf("reading the token: %w", err)
		}
		if tok = strings.TrimSpace(tok); tok == "" {
			return nil, ErrNoToken
		}
		env[config.EnvToken] = tok
	}
	return env, nil
}

// askProject asks for the project id when neither the settings nor the config
// file set it. Without a terminal, or with an empty answer, it stays unset and
// the pre-check reports it; so does a config file that does not load.
func askProject(o SettingsOptions, env map[string]string) error {
	if env[config.EnvProjectID] != "" {
		return nil
	}
	s, err := config.LoadWithDefault("", o.ConfigPath, func(k string) string { return env[k] })
	if err != nil || s.ProjectID != "" {
		return nil // a broken config is the pre-check's to report
	}
	id, err := o.Prompt.PromptLine("Omnismith project id: ")
	switch {
	case errors.Is(err, ErrNotInteractive):
		return nil
	case err != nil:
		return fmt.Errorf("reading the project id: %w", err)
	}
	if id = strings.TrimSpace(id); id != "" {
		env[config.EnvProjectID] = id
	}
	return nil
}
