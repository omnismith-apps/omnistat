package systemd_test

import (
	"os"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/systemd"
)

// FR-006–FR-010, FR-013, FR-023: the unit install writes, byte for byte. The
// golden file is the reviewed artifact; change both together.
func TestUnitText_Golden(t *testing.T) {
	want, err := os.ReadFile("testdata/omnistat.service")
	if err != nil {
		t.Fatal(err)
	}
	if got := systemd.UnitText(); got != string(want) {
		t.Fatalf("unit text differs from testdata/omnistat.service:\n%s", got)
	}
}

// FR-019: the marker is the first line, and it is how install recognises its
// own unit.
func TestUnitText_Marker(t *testing.T) {
	if !strings.HasPrefix(systemd.UnitText(), systemd.Marker+"\n") || !systemd.OwnUnit([]byte(systemd.UnitText())) {
		t.Fatal("the unit must start with the marker")
	}
	for _, foreign := range []string{"", "[Unit]\nDescription=omnistat\n", "# omnistat\n" + systemd.Marker + "\n"} {
		if systemd.OwnUnit([]byte(foreign)) {
			t.Errorf("not ours: %q", foreign)
		}
	}
}

// FR-006, FR-012, FR-013: the unit runs the installed binary with the service
// config and reads the stored settings from their file.
func TestUnitText_Paths(t *testing.T) {
	u := systemd.UnitText()
	for _, want := range []string{
		"ExecStart=" + systemd.BinaryPath + " --config " + systemd.ConfigPath + " run --daemon\n",
		"EnvironmentFile=" + systemd.EnvPath + "\n",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q", want)
		}
	}
}
