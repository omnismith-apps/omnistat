package systemd_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/systemd"
	"github.com/omnismith-apps/omnistat/internal/systemd/fakehost"
)

// Spec 009 FR-002, FR-003, FR-012: what upgrade learns about the installed
// service, under install's preconditions, and never by changing anything.
func TestInstallation(t *testing.T) {
	stored := map[string]string{"OMNISMITH_ACCESS_TOKEN": secret, "HTTPS_PROXY": "http://u:PROXY_PW@proxy:3128"} //nolint:gosec // fake sentinel, asserted never printed
	for name, tc := range map[string]struct {
		mod    func(*fakehost.Host)
		exists bool
		err    error
		msg    string
	}{
		"installed":     {mod: func(h *fakehost.Host) { installed(h, stored) }, exists: true},
		"not installed": {mod: func(*fakehost.Host) {}},
		"not root":      {mod: func(h *fakehost.Host) { installed(h, stored); h.IsRoot = false }, err: systemd.ErrNotRoot},
		"no systemd":    {mod: func(h *fakehost.Host) { h.IsBooted = false }, err: systemd.ErrNotBooted},
		"foreign unit": {mod: func(h *fakehost.Host) {
			h.Files[systemd.UnitPath] = fakehost.Entry{Data: []byte("[Service]\nExecStart=/opt/x\n"), Mode: 0o644}
		}, err: systemd.ErrForeignUnit},
		"broken settings": {mod: func(h *fakehost.Host) {
			installed(h, stored)
			h.Files[systemd.EnvPath] = fakehost.Entry{Data: []byte("not an assignment " + secret + "\n"), Mode: 0o600}
		}, msg: "line 1"},
	} {
		t.Run(name, func(t *testing.T) {
			h := fakehost.New("/home/op/omnistat")
			tc.mod(h)
			in, err := systemd.Installation(context.Background(), h)
			switch {
			case tc.err != nil || tc.msg != "":
				if err == nil || (tc.err != nil && !errors.Is(err, tc.err)) || !strings.Contains(err.Error(), tc.msg) {
					t.Fatalf("%v", err)
				}
				noSecrets(t, err.Error())
			case err != nil:
				t.Fatal(err)
			case in.Exists != tc.exists:
				t.Fatalf("%+v", in)
			case tc.exists && (in.Binary != systemd.BinaryPath || in.Settings["HTTPS_PROXY"] != stored["HTTPS_PROXY"]):
				t.Fatalf("%+v", in)
			}
			if calls := h.Calls(); len(calls) != 0 {
				t.Fatalf("changed something: %v", calls)
			}
		})
	}
}
