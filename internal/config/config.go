// Package config loads omnistat's YAML configuration and environment
// (spec 001 FR-006…010). The access token is accepted from the environment
// only (constitution IV); a config file that contains one is rejected.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// Environment variable names.
const (
	EnvToken     = "OMNISMITH_ACCESS_TOKEN"
	EnvProjectID = "OMNISMITH_PROJECT_ID"
	EnvBaseURL   = "OMNISMITH_BASE_URL"
)

// DefaultPath is the config file used when none is given and it exists.
const DefaultPath = "omnistat.yaml"

// Mode is the reconciliation mode (FR-010).
type Mode string

const (
	ModeApply  Mode = "apply"
	ModeVerify Mode = "verify"
	ModeOff    Mode = "off"
)

// Settings is the validated, merged configuration.
type Settings struct {
	// Path is the config file that was read, or "" for none.
	Path      string
	Token     string
	ProjectID string
	BaseURL   string
	Mode      Mode
	Overrides manifest.Overrides
	// Modules holds explicit on/off switches (absent = module default).
	Modules map[string]bool
	HTTP    struct {
		Timeout time.Duration
		Retries int
	}
	Log struct {
		Level  string
		Format string
	}
}

// RequireAPI checks that the settings can reach the API (token and project).
func (s Settings) RequireAPI() error {
	var problems []error
	if strings.TrimSpace(s.Token) == "" {
		problems = append(problems, fmt.Errorf("access token missing: set %s", EnvToken))
	}
	if strings.TrimSpace(s.ProjectID) == "" {
		problems = append(problems, fmt.Errorf("project id missing: set %s or project_id in %s", EnvProjectID, DefaultPath))
	}
	return errors.Join(problems...)
}

// file is the YAML shape. Unknown keys are errors (strict), so typos surface.
type file struct {
	ProjectID   string `yaml:"project_id"`
	BaseURL     string `yaml:"base_url"`
	AccessToken string `yaml:"access_token"` // rejected if set; present only to name it in the error
	Schema      struct {
		Mode         string `yaml:"mode"`
		HostTemplate string `yaml:"host_template"`
	} `yaml:"schema"`
	Modules map[string]moduleFile `yaml:"modules"`
	HTTP    struct {
		Timeout string `yaml:"timeout"`
		Retries *int   `yaml:"retries"`
	} `yaml:"http"`
	Log struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	} `yaml:"log"`
}

type moduleFile struct {
	Enabled    *bool                    `yaml:"enabled"`
	Template   string                   `yaml:"template"`
	Attributes map[string]attributeFile `yaml:"attributes"`
}

type attributeFile struct {
	Slug        string `yaml:"slug"`
	Template    string `yaml:"template"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Load reads path (or DefaultPath when path is "" and it exists), applies
// environment overrides via getenv, validates, and returns Settings. Missing
// token/project are not errors here; see Settings.RequireAPI.
func Load(path string, getenv func(string) string) (Settings, error) {
	var s Settings
	s.Modules = map[string]bool{}
	s.BaseURL = "https://api.omnismith.io/v1"
	s.Mode = ModeApply
	s.HTTP.Timeout = 15 * time.Second
	s.HTTP.Retries = 3
	s.Log.Level, s.Log.Format = "info", "text"

	var f file
	if path == "" {
		if _, err := os.Stat(DefaultPath); err == nil {
			path = DefaultPath
		}
	}
	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // operator-supplied config path
		if err != nil {
			return s, fmt.Errorf("config: %w", err)
		}
		if err := yaml.UnmarshalWithOptions(data, &f, yaml.Strict()); err != nil {
			return s, fmt.Errorf("config %s: %w", path, err)
		}
		s.Path = path
	}

	var problems []error
	add := func(format string, args ...any) { problems = append(problems, fmt.Errorf(format, args...)) }

	if f.AccessToken != "" {
		add("access_token must not be in the config file; set %s instead", EnvToken)
	}
	s.Token = getenv(EnvToken)
	s.ProjectID = f.ProjectID
	if v := getenv(EnvProjectID); v != "" {
		s.ProjectID = v
	}
	if f.BaseURL != "" {
		s.BaseURL = f.BaseURL
	}
	if v := getenv(EnvBaseURL); v != "" {
		s.BaseURL = v
	}
	if !strings.HasPrefix(s.BaseURL, "http://") && !strings.HasPrefix(s.BaseURL, "https://") {
		add("base_url %q must start with http:// or https://", s.BaseURL)
	}

	if f.Schema.Mode != "" {
		switch m := Mode(f.Schema.Mode); m {
		case ModeApply, ModeVerify, ModeOff:
			s.Mode = m
		default:
			add("schema.mode %q must be one of apply, verify, off", f.Schema.Mode)
		}
	}
	if f.Schema.HostTemplate != "" {
		if !manifest.ValidSlug(f.Schema.HostTemplate) {
			add("schema.host_template %q must match ^[a-z][a-z0-9_]*$", f.Schema.HostTemplate)
		}
		s.Overrides.HostTemplate = f.Schema.HostTemplate
	}

	if len(f.Modules) > 0 {
		s.Overrides.Modules = map[string]manifest.ModuleOverride{}
	}
	for name, mf := range f.Modules {
		if mf.Enabled != nil {
			s.Modules[name] = *mf.Enabled
		}
		mo := manifest.ModuleOverride{Template: mf.Template}
		if len(mf.Attributes) > 0 {
			mo.Attributes = map[string]manifest.AttributeOverride{}
		}
		for key, af := range mf.Attributes {
			mo.Attributes[key] = manifest.AttributeOverride{Slug: af.Slug, Template: af.Template, Name: af.Name, Description: af.Description}
		}
		s.Overrides.Modules[name] = mo
	}

	if f.HTTP.Timeout != "" {
		d, err := time.ParseDuration(f.HTTP.Timeout)
		switch {
		case err != nil:
			add("http.timeout %q: %v", f.HTTP.Timeout, err)
		case d <= 0:
			add("http.timeout must be positive, got %s", d)
		default:
			s.HTTP.Timeout = d
		}
	}
	if f.HTTP.Retries != nil {
		if *f.HTTP.Retries < 0 {
			add("http.retries must be >= 0, got %d", *f.HTTP.Retries)
		} else {
			s.HTTP.Retries = *f.HTTP.Retries
		}
	}
	if f.Log.Level != "" {
		switch f.Log.Level {
		case "debug", "info", "warn", "error":
			s.Log.Level = f.Log.Level
		default:
			add("log.level %q must be one of debug, info, warn, error", f.Log.Level)
		}
	}
	if f.Log.Format != "" {
		switch f.Log.Format {
		case "text", "json":
			s.Log.Format = f.Log.Format
		default:
			add("log.format %q must be text or json", f.Log.Format)
		}
	}
	if err := errors.Join(problems...); err != nil {
		return s, fmt.Errorf("config: %w", err)
	}
	return s, nil
}
