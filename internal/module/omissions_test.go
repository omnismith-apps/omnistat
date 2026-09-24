package module_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/module"
)

func captureLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// 004 FR-016, 005 FR-013: every omission of a collection lands in one record
// that names each key and its reason.
func TestOmissions_OneRecordPerCollection(t *testing.T) {
	log, buf := captureLog()
	var o module.Omissions
	o.Add("load1", errors.New("no load average"))
	o.Add("model", errors.New("no model reported"))
	o.Log(log, "cpu")

	out := buf.String()
	if n := strings.Count(out, "observations omitted"); n != 1 {
		t.Fatalf("want exactly one record, got %d:\n%s", n, out)
	}
	for _, want := range []string{
		"level=ERROR", "module=cpu", "keys=load1,model",
		`reasons="load1: no load average; model: no model reported"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("record lacks %q:\n%s", want, out)
		}
	}
	if o.Len() != 2 {
		t.Errorf("Len = %d, want 2", o.Len())
	}
}

// A healthy collection is silent.
func TestOmissions_NothingOmittedLogsNothing(t *testing.T) {
	log, buf := captureLog()
	var o module.Omissions
	o.Log(log, "memory")
	if buf.Len() != 0 {
		t.Fatalf("expected no record, got:\n%s", buf.String())
	}
	if o.Len() != 0 {
		t.Errorf("Len = %d, want 0", o.Len())
	}
}

// 004 FR-015, 005 FR-012: the failure of a collection that read nothing names
// the module and every reason.
func TestOmissions_Err(t *testing.T) {
	var empty module.Omissions
	if got, want := empty.Err("memory").Error(), "memory: nothing could be read"; got != want {
		t.Errorf("empty: got %q, want %q", got, want)
	}
	var o module.Omissions
	o.Add("usage", errors.New("cpu times: boom"))
	o.Add("cores", errors.New("cpu count: boom"))
	want := "cpu: nothing could be read: usage: cpu times: boom; cores: cpu count: boom"
	if got := o.Err("cpu").Error(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
