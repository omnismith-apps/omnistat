package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/systemd"
)

// 007 FR-022: under the journal every log record carries its priority, and so
// does plain stderr output such as the final error.
func TestJournalLogging(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	var journal, errb, out bytes.Buffer
	app := &cli.App{Registry: registryWithMachineID(fstest.MapFS{"etc/machine-id": {Data: []byte(rawID)}}), Version: "t", Journal: &journal}
	env := apiEnv(srv)
	code := app.Run(context.Background(), []string{"--log-level", "debug", "run", "--dry-run"}, &out, systemd.PriorityWriter(&errb, systemd.PrioErr), func(k string) string { return env[k] })
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, errb.String(), journal.String())
	}
	lines := strings.Split(strings.TrimSuffix(journal.String(), "\n"), "\n")
	if len(lines) < 2 || !strings.Contains(journal.String(), "<6>time=") || !strings.Contains(journal.String(), "<7>time=") {
		t.Fatalf("info and debug records with priorities:\n%s", journal.String())
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "<") {
			t.Fatalf("every line has a priority: %q", l)
		}
	}

	errb.Reset()
	env["OMNISMITH_ACCESS_TOKEN"] = ""
	if code := app.Run(context.Background(), []string{"run"}, &out, systemd.PriorityWriter(&errb, systemd.PrioErr), func(k string) string { return env[k] }); code != 1 || !strings.HasPrefix(errb.String(), "<3>omnistat: ") {
		t.Fatalf("the final error is an err record: %d %q", code, errb.String())
	}
}
