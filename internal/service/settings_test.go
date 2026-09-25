package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/service"
)

const token = "omni_live_SENTINEL" //nolint:gosec // fake sentinel

// prompter answers the prompts and counts them.
type prompter struct {
	secret, line       string
	secretErr, lineErr error
	secrets, lines     int
}

func (p *prompter) PromptSecret(string) (string, error) { p.secrets++; return p.secret, p.secretErr }
func (p *prompter) PromptLine(string) (string, error)   { p.lines++; return p.line, p.lineErr }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func configWith(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "omnistat.yaml")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// 007 FR-017 (also 006, amended): the project id comes from the environment,
// the stored settings or the config file; only when none has it is the
// operator asked, and the answer is stored with the other settings.
func TestResolveSettings_ProjectID(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stored    map[string]string
		getenv    map[string]string
		config    string
		p         prompter
		want      string
		wantAsked bool
	}{
		{name: "environment", getenv: map[string]string{"OMNISMITH_PROJECT_ID": "p-env"}, want: "p-env"},
		{name: "stored", stored: map[string]string{"OMNISMITH_PROJECT_ID": "p-stored"}, want: "p-stored"},
		{name: "config file", config: "project_id: p-file\n", want: ""},
		{name: "asked", p: prompter{line: "  p-typed \n"}, want: "p-typed", wantAsked: true},
		{name: "no terminal", p: prompter{lineErr: service.ErrNotInteractive}, want: "", wantAsked: true},
		{name: "empty answer", p: prompter{line: "  "}, want: "", wantAsked: true},
		{name: "config broken: the pre-check reports it", config: "project_id: [\n", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.p.secret = token
			got, err := service.ResolveSettings(service.SettingsOptions{
				Stored: tc.stored, Getenv: env(tc.getenv), Names: service.Captured,
				ConfigPath: configWith(t, tc.config), Prompt: &tc.p,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got["OMNISMITH_PROJECT_ID"] != tc.want || (tc.p.lines == 1) != tc.wantAsked || tc.p.lines > 1 {
				t.Fatalf("project %q (want %q), asked %d times", got["OMNISMITH_PROJECT_ID"], tc.want, tc.p.lines)
			}
		})
	}
}

// A prompt failure other than "no terminal" is an error.
func TestResolveSettings_ProjectPromptError(t *testing.T) {
	broken := errors.New("read error")
	_, err := service.ResolveSettings(service.SettingsOptions{Names: service.Captured, ConfigPath: configWith(t, ""), Prompt: &prompter{secret: token, lineErr: broken}})
	if !errors.Is(err, broken) {
		t.Fatalf("%v", err)
	}
}

// 006 FR-013/014/018: capture, merge and the token prompt.
func TestResolveSettings_Token(t *testing.T) {
	cfg := configWith(t, "")
	base := map[string]string{"OMNISMITH_PROJECT_ID": "p1"}

	p := &prompter{secret: "  " + token + "  "}
	got, err := service.ResolveSettings(service.SettingsOptions{Getenv: env(base), Names: service.Captured, ConfigPath: cfg, Prompt: p})
	if err != nil || got["OMNISMITH_ACCESS_TOKEN"] != token || p.secrets != 1 {
		t.Fatalf("prompted and trimmed: %v %d", err, p.secrets)
	}

	p = &prompter{}
	stored := map[string]string{"OMNISMITH_ACCESS_TOKEN": "omni_old", "OMNISMITH_PROJECT_ID": "p1", "HTTPS_PROXY": "http://old:3128"}
	got, err = service.ResolveSettings(service.SettingsOptions{Stored: stored, Getenv: env(map[string]string{"HTTPS_PROXY": "http://new:3128", "PATH": "/bin"}), Names: service.Captured, ConfigPath: cfg, Prompt: p})
	if err != nil || p.secrets != 0 || got["OMNISMITH_ACCESS_TOKEN"] != "omni_old" || got["HTTPS_PROXY"] != "http://new:3128" || got["PATH"] != "" {
		t.Fatalf("stored kept, set replaced, others ignored: %v %v", err, got)
	}
	if stored["HTTPS_PROXY"] != "http://old:3128" {
		t.Fatal("the stored map must not be modified")
	}

	p = &prompter{secret: token}
	got, err = service.ResolveSettings(service.SettingsOptions{Stored: stored, Names: service.Captured, ConfigPath: cfg, ReplaceToken: true, Prompt: p})
	if err != nil || p.secrets != 1 || got["OMNISMITH_ACCESS_TOKEN"] != token {
		t.Fatalf("--replace-token asks: %v %d", err, p.secrets)
	}

	for name, p := range map[string]*prompter{"no terminal": {secretErr: service.ErrNotInteractive}, "empty": {secret: " "}} {
		if _, err := service.ResolveSettings(service.SettingsOptions{Getenv: env(base), Names: service.Captured, ConfigPath: cfg, Prompt: p}); !errors.Is(err, service.ErrNoToken) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// 007 FR-013: a backend's extra names (Linux: lower-case proxies) are captured
// as spelled.
func TestResolveSettings_Names(t *testing.T) {
	got, err := service.ResolveSettings(service.SettingsOptions{
		Getenv: env(map[string]string{"OMNISMITH_ACCESS_TOKEN": token, "OMNISMITH_PROJECT_ID": "p1", "https_proxy": "http://p:3128"}),
		Names:  append([]string{"https_proxy"}, service.Captured...), ConfigPath: configWith(t, ""), Prompt: &prompter{},
	})
	if err != nil || got["https_proxy"] != "http://p:3128" || got["HTTPS_PROXY"] != "" {
		t.Fatalf("%v %v", err, got)
	}
}
