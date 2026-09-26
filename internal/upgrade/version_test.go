package upgrade_test

import (
	"testing"

	"github.com/omnismith-apps/omnistat/internal/upgrade"
)

func mustVersion(t *testing.T, s string) upgrade.Version {
	t.Helper()
	v, ok := upgrade.ParseVersion(s)
	if !ok {
		t.Fatalf("ParseVersion(%q) failed", s)
	}
	return v
}

// FR-005: release versions parse with or without "v"; development builds and
// garbage are not release versions.
func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string // "" = not a release version
	}{
		{"v0.3.0", "v0.3.0"},
		{"0.3.0", "v0.3.0"},
		{" v1.12.0\n", "v1.12.0"},
		{"v0.5.0-rc.1", "v0.5.0-rc.1"},
		{"0.2.0-beta", "v0.2.0-beta"},
		{"dev", ""},
		{"v0.3.0-3-gd38c574", ""},
		{"v0.3.0-3-gd38c574-dirty", ""},
		{"v0.3.0-dirty", ""},
		{"d38c574", ""},
		{"v0.3", ""},
		{"v01.2.3", ""},
		{"v1.2.3+meta", ""},
		{"", ""},
	} {
		v, ok := upgrade.ParseVersion(tc.in)
		got := ""
		if ok {
			got = v.String()
		}
		if got != tc.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if n := mustVersion(t, "v0.5.0-rc.1").Number(); n != "0.5.0-rc.1" {
		t.Errorf("Number() = %q", n)
	}
}

// FR-007: SemVer precedence.
func TestCompare(t *testing.T) {
	ordered := []string{"v0.2.0", "v0.3.0-alpha", "v0.3.0-alpha.1", "v0.3.0-alpha.beta", "v0.3.0-beta.2", "v0.3.0-beta.11", "v0.3.0-rc.1", "v0.3.0", "v0.3.1", "v0.10.0", "v1.0.0"}
	for i, si := range ordered {
		for j, sj := range ordered {
			a, b := mustVersion(t, si), mustVersion(t, sj)
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := upgrade.Compare(a, b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", si, sj, got, want)
			}
		}
	}
}

// FR-005: the installed binary's `omnistat version` output.
func TestParseVersionOutput(t *testing.T) {
	for in, want := range map[string]bool{
		"omnistat v0.3.0\n":                  true,
		"omnistat 0.3.0":                     true,
		"omnistat dev\n":                     false,
		"omnistat v0.3.0-3-gd38c574-dirty\n": false,
		"something else v0.3.0":              false,
		"":                                   false,
	} {
		if _, ok := upgrade.ParseVersionOutput(in); ok != want {
			t.Errorf("ParseVersionOutput(%q) ok = %v, want %v", in, ok, want)
		}
	}
}
