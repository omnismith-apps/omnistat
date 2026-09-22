//go:build sandbox

package cli_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/hostname"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/omni"
)

// TestSandbox_Run runs the real one-shot `run` against a real project with a
// throwaway static identity: it creates (or reuses) one host entity and
// publishes the hostname, then checks the value through the API. Nothing is
// deleted (constitution IV). Needs OMNISMITH_ACCESS_TOKEN / _PROJECT_ID /
// _BASE_URL and a reconciled schema.
//
//	make sandbox
func TestSandbox_Run(t *testing.T) {
	s, err := config.Load("", os.Getenv)
	if err != nil || s.RequireAPI() != nil {
		t.Skip("sandbox credentials not set")
	}
	reg := module.NewRegistry()
	reg.Register(machineid.New(), module.Required())
	hn := hostname.New()
	hn.Hostname = func() (string, error) { return "sandbox-host", nil }
	reg.Register(hn)
	app := &cli.App{Registry: reg, Version: "sandbox"}
	env := func(k string) string {
		if k == config.EnvIdentity {
			return "sandbox-run"
		}
		return os.Getenv(k)
	}

	var out, errb bytes.Buffer
	if code := app.Run(context.Background(), []string{"run", "--dry-run"}, &out, &errb, env); code != 0 || !strings.Contains(out.String(), `hostname = "sandbox-host"`) {
		t.Fatalf("dry-run: code=%d\n%s%s", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := app.Run(context.Background(), []string{"run"}, &out, &errb, env); code != 0 || !strings.Contains(out.String(), "published 1 dimensions, 0 observations") {
		t.Fatalf("run: code=%d\n%s%s", code, out.String(), errb.String())
	}
	t.Log(strings.TrimSpace(out.String()))

	// Read back: the entity with identity "sandbox-run" carries the hostname.
	api, err := omni.New(omni.Settings{BaseURL: s.BaseURL, Token: s.Token, ProjectID: s.ProjectID, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := api.ReadSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found, err := api.FindEntities(context.Background(), cur.Templates["host"].ID, "machine_id", "sandbox-run")
	if err != nil || len(found) == 0 {
		t.Fatalf("find: %v %v", found, err)
	}
}
