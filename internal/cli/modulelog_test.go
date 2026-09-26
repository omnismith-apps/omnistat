package cli_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/moduletest"
)

// 003 FR-026 (amended 2026-09-26, found in spec 008): what a module logs —
// omission records, notices, debug records — goes through the process logger,
// with its level, format and destination, not through Go's default logger,
// which ignored log.level and log.format and bypassed the journal and the
// Windows Event Log.
func TestRun_ModuleLogsUseTheProcessLogger(t *testing.T) {
	h := newHarness(t, "")
	h.reg.Register(moduletest.WithProvider(moduletest.Volume(), time.Minute, func(context.Context) ([]module.Observation, error) {
		slog.Default().Debug("module debug record", "module", "volume")
		slog.Default().Error("observations omitted", "module", "volume")
		return []module.Observation{{Key: "count", Value: 1}}, nil
	}))
	before := slog.Default()

	runWith := func(flags ...string) string {
		var out, errb bytes.Buffer
		args := append(append([]string{"--config", h.cfg}, flags...), "run", "--dry-run")
		if code := h.app().Run(context.Background(), args, &out, &errb, func(k string) string { return h.env[k] }); code != 0 {
			t.Fatalf("run %v: code %d\n%s%s", flags, code, out.String(), errb.String())
		}
		return errb.String()
	}

	logs := runWith("--log-level", "debug")
	for _, want := range []string{
		`level=DEBUG msg="module debug record" module=volume`,
		`level=ERROR msg="observations omitted" module=volume`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("debug run lacks %q:\n%s", want, logs)
		}
	}

	logs = runWith("--log-level", "warn", "--log-format", "json")
	if strings.Contains(logs, "module debug record") {
		t.Errorf("a debug record must honour log.level:\n%s", logs)
	}
	if !strings.Contains(logs, `"level":"ERROR","msg":"observations omitted","module":"volume"`) {
		t.Errorf("an error record must honour log.format:\n%s", logs)
	}

	if slog.Default() != before {
		t.Fatal("Run must restore the default logger it replaced")
	}
}
