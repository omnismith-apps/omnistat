package upgrade_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/upgrade"
)

// The test binary doubles as a fake omnistat when this is set: it prints its
// arguments and an inherited variable, and exits with the status asked for.
const helperEnv = "OMNISTAT_UPGRADE_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		args := os.Args[1:]
		if len(args) == 1 && args[0] == "version" {
			fmt.Println("omnistat v0.4.0")
			os.Exit(0)
		}
		fmt.Printf("args=%s inherited=%s\n", strings.Join(args, ","), os.Getenv("OMNISTAT_INHERITED"))
		fmt.Fprintln(os.Stderr, "to stderr")
		if len(args) > 0 && args[len(args)-1] == "--fail" {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// FR-009, FR-013: the real runner reads the version, runs install with
// upgrade's environment and output, and returns its exit status.
func TestExecRunner(t *testing.T) {
	t.Setenv(helperEnv, "1")
	t.Setenv("OMNISTAT_INHERITED", "yes")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := upgrade.ExecRunner{}
	out, err := r.Version(context.Background(), self)
	if v, ok := upgrade.ParseVersionOutput(out); err != nil || !ok || v.String() != "v0.4.0" {
		t.Fatalf("version: %q %v", out, err)
	}
	var stdout, stderr bytes.Buffer
	code, err := r.Install(self, []string{"service", "install", "--dry-run"}, &stdout, &stderr)
	if err != nil || code != 0 || stdout.String() != "args=service,install,--dry-run inherited=yes\n" || stderr.String() != "to stderr\n" {
		t.Fatalf("%d %v %q %q", code, err, stdout.String(), stderr.String())
	}
	if code, err = r.Install(self, []string{"--fail"}, &stdout, &stderr); err != nil || code != 3 {
		t.Fatalf("exit status: %d %v", code, err)
	}
	if _, err = r.Install(self+"-missing", nil, &stdout, &stderr); err == nil {
		t.Fatal("a missing binary is an error")
	}
}
