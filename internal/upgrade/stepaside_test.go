package upgrade_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/upgrade"
)

func TestRunsFrom(t *testing.T) {
	const installed = `C:\Program Files\omnistat\omnistat.exe`
	for _, tc := range []struct {
		self, goos string
		want       bool
	}{
		{installed, "windows", true},
		{`c:\program files\OMNISTAT\omnistat.exe`, "windows", true},
		{`C:\Users\op\Downloads\omnistat.exe`, "windows", false},
		{"/usr/local/bin/omnistat", "linux", false}, // Linux replaces a running binary fine
	} {
		inst := installed
		if tc.goos == "linux" {
			inst = tc.self
		}
		if got := upgrade.RunsFrom(tc.self, inst, tc.goos); got != tc.want {
			t.Errorf("RunsFrom(%q, %s) = %v", tc.self, tc.goos, got)
		}
	}
}

// Spec 009 edge case (Windows, from the installed copy): the running binary
// makes way for the install; it comes back only if the install left nothing.
func TestStepAside(t *testing.T) {
	setup := func(t *testing.T) (self, stage string) {
		dir := t.TempDir()
		self = filepath.Join(dir, "omnistat.exe")
		if err := os.WriteFile(self, []byte("previous"), 0o600); err != nil {
			t.Fatal(err)
		}
		stage, cleanup, err := upgrade.Stage(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		return self, stage
	}

	self, stage := setup(t)
	a, err := upgrade.StepAside(self, stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(self); !os.IsNotExist(err) || filepath.Dir(a.Moved()) != stage {
		t.Fatalf("the installed path must be free, moved to %s", a.Moved())
	}
	if restored, err := a.Finish(); err != nil || !restored {
		t.Fatalf("an install that left nothing: restored %v, %v", restored, err)
	}
	if data, _ := os.ReadFile(self); string(data) != "previous" { //nolint:gosec // test file
		t.Fatalf("restored %q", data)
	}

	self, stage = setup(t)
	if a, err = upgrade.StepAside(self, stage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(self, []byte("new"), 0o600); err != nil { // what install does
		t.Fatal(err)
	}
	if restored, err := a.Finish(); err != nil || restored {
		t.Fatalf("an install that put its binary: restored %v, %v", restored, err)
	}
	if data, _ := os.ReadFile(self); string(data) != "new" { //nolint:gosec // test file
		t.Fatalf("the new binary was replaced: %q", data)
	}
}
