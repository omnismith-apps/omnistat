package hostread

import (
	"os"
	"path/filepath"
	"testing"
)

// Spec 010 FR-005: /sys/class/net lists every interface in the namespace. One
// with a `device` link is backed by a NIC (hardware, or one a hypervisor
// presents); one without is software. A device-backed interface whose master
// is itself device-backed (an SR-IOV VF enslaved to Azure's synthetic NIC) is
// already counted by that master.
func TestClassifyNetIn(t *testing.T) {
	root := t.TempDir()
	mk := func(name string, device bool, master string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if device {
			// The real entry is a symlink into /sys/devices; any entry will do.
			if err := os.Mkdir(filepath.Join(dir, "device"), 0o750); err != nil {
				t.Fatal(err)
			}
		}
		if master != "" {
			if err := os.Symlink(filepath.Join("..", master), filepath.Join(dir, "master")); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("eth0", true, "")
	mk("wlo1", true, "")
	mk("lo", false, "")
	mk("docker0", false, "")
	mk("veth1", false, "docker0")
	mk("bond0", false, "")
	mk("ens1", true, "bond0") // bond member: counted, the bond is not
	mk("ens2", true, "bond0")
	mk("br0", false, "")
	mk("enp3s0", true, "br0") // bridge port: counted
	mk("eth0.100", false, "")
	mk("wg0", false, "")
	mk("enP1s1", true, "eth0") // Azure AN: the VF's traffic is in eth0's counters

	for _, tc := range []struct {
		name string
		want bool
	}{
		{"eth0", true},
		{"wlo1", true},
		{"lo", false},
		{"docker0", false},
		{"veth1", false},
		{"bond0", false},
		{"ens1", true},
		{"ens2", true},
		{"br0", false},
		{"enp3s0", true},
		{"eth0.100", false},
		{"wg0", false},
		{"enP1s1", false},
		{"gone0", false}, // listed in /proc/net/dev, removed before the stat
	} {
		if got := classifyNetIn(root, tc.name); got != tc.want {
			t.Errorf("classifyNetIn(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
