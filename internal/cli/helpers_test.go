package cli_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

// exec2 runs the CLI with an explicit registry against srv.
func exec2(t *testing.T, srv *omnitest.Server, reg *module.Registry, args ...string) run {
	t.Helper()
	env := map[string]string{"OMNISMITH_ACCESS_TOKEN": omnitest.Token, "OMNISMITH_PROJECT_ID": omnitest.ProjectID, "OMNISMITH_BASE_URL": srv.URL}
	app := &cli.App{Registry: reg, Version: "t"}
	var out, errb bytes.Buffer
	code := app.Run(context.Background(), args, &out, &errb, func(k string) string { return env[k] })
	return run{code, out.String(), errb.String()}
}
