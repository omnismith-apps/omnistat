package systemd_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/systemd"
)

// FR-013: every value survives the round trip through the file systemd reads,
// whatever it contains.
func TestEnvFile_RoundTrip(t *testing.T) {
	in := map[string]string{
		"OMNISMITH_ACCESS_TOKEN": "omni_live_abc123",
		"OMNISMITH_PROJECT_ID":   "01a0c5ef-6b6b-7368-acd7-be2359db2305",
		"HTTPS_PROXY":            "http://us%40r:p'a\"s$s`w\\d@proxy:3128",
		"https_proxy":            "http://p:3128",
		"NO_PROXY":               "localhost, .corp  ",
		"OMNISTAT_IDENTITY":      "it's %h $HOME",
		"EMPTY":                  "",
	}
	data, err := systemd.FormatEnv(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "#") {
		t.Fatalf("the file starts with a comment saying what it is:\n%s", data)
	}
	out, err := systemd.ParseEnv(data)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(in, out) {
		t.Fatalf("round trip:\n in %q\nout %q\nfile:\n%s", in, out, data)
	}
	// Sorted, one per line, so the file diffs cleanly between installs.
	if !strings.Contains(string(data), "HTTPS_PROXY=") || strings.Index(string(data), "EMPTY=") > strings.Index(string(data), "NO_PROXY=") {
		t.Fatalf("sorted:\n%s", data)
	}
}

// FR-013: a value systemd cannot hold on one line, or a bad name, is refused;
// the error names the setting, never the value.
func TestEnvFile_FormatRejects(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"newline":  {"OMNISMITH_ACCESS_TOKEN": "omni_SECRET\nX=1"}, //nolint:gosec // fake sentinel, asserted never printed
		"cr":       {"OMNISMITH_ACCESS_TOKEN": "omni_SECRET\r"},
		"nul":      {"OMNISMITH_ACCESS_TOKEN": "omni_SECRET\x00"},
		"bad name": {"1BAD": "omni_SECRET"},
	} {
		_, err := systemd.FormatEnv(m)
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Spec edge case: the file as systemd reads it, including hand edits. Every
// form and expected value here was checked against systemd 259 (systemd-run
// --user -p EnvironmentFile=…).
func TestEnvFile_ParseSystemdForms(t *testing.T) {
	data := "# comment\n; also a comment\n\n" +
		"A='sq $HOME \"x\" \\n'\n" +
		"B=\"dq \\\"q\\\" \\\\ \\$HOME \\`t\\` it's \\n\"\n" +
		"C=plain value  \n" +
		"  D =  spaced\n" +
		"E=\"%h %n\"\n" +
		"F=a'b c'\"d\"\n" +
		"H='a' 'b'\"c\"x'y'  \n" +
		"I=a\\'b\n" +
		"G=esc\\ aped\n" +
		"A=last wins\n"
	got, err := systemd.ParseEnv([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"A": "last wins",
		"B": `dq "q" \ $HOME ` + "`t`" + ` it's \n`,
		"C": "plain value",
		"D": "spaced",
		"E": "%h %n",
		"F": `a'b c'"d"`,
		"H": "abcx'y'",
		"I": "a'b",
		"G": "esc aped",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// Spec edge case: a line install cannot read is an error naming its number,
// never its content.
func TestEnvFile_ParseErrors(t *testing.T) {
	for name, data := range map[string]string{
		"no equals":    "OK=1\nomni_SECRET\n",
		"unterminated": "OK=1\nOMNISMITH_ACCESS_TOKEN='omni_SECRET\n",
		"bad name":     "OK=1\nomni-SECRET=x\n",
		"continuation": "OK=1\nOMNISMITH_ACCESS_TOKEN=omni_SECRET\\\n",
	} {
		_, err := systemd.ParseEnv([]byte(data))
		if err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
