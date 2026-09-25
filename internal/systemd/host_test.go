package systemd_test

import (
	"reflect"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/systemd"
)

// Unit introspection: `systemctl show` for a missing, a loaded and a masked unit.
func TestParseShow(t *testing.T) {
	for name, tc := range map[string]struct {
		out  string
		want systemd.Unit
	}{
		"not found": {"LoadState=not-found\nFragmentPath=\nActiveState=inactive\nSubState=dead\nDropInPaths=\n",
			systemd.Unit{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}},
		"loaded": {"LoadState=loaded\nFragmentPath=/etc/systemd/system/omnistat.service\nActiveState=active\nSubState=running\n" +
			"DropInPaths=/etc/systemd/system/omnistat.service.d/override.conf /run/systemd/system/omnistat.service.d/x.conf\n",
			systemd.Unit{LoadState: "loaded", FragmentPath: "/etc/systemd/system/omnistat.service", ActiveState: "active", SubState: "running",
				DropInPaths: []string{"/etc/systemd/system/omnistat.service.d/override.conf", "/run/systemd/system/omnistat.service.d/x.conf"}}},
		"masked": {"LoadState=masked\nFragmentPath=/etc/systemd/system/omnistat.service\nActiveState=inactive\nSubState=dead\n",
			systemd.Unit{LoadState: "masked", FragmentPath: "/etc/systemd/system/omnistat.service", ActiveState: "inactive", SubState: "dead"}},
	} {
		if got := systemd.ParseShow(tc.out); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
