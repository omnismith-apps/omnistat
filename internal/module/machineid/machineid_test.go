package machineid_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
)

const (
	rawLinux   = "b3f1c2d4e5f60718293a4b5c6d7e8f90"
	rawDarwin  = "3C2E7A55-1B9D-4E4F-9F1A-0C2B3D4E5F60"
	rawWindows = "5d8f2a1c-7e3b-4c9d-a6f0-1b2c3d4e5f60"
)

func mod(goos string, fsys fstest.MapFS, out string, runErr error) *machineid.Module {
	m := machineid.New()
	m.GOOS = goos
	m.FS = fsys
	m.IOReg = func(context.Context) ([]byte, error) { return []byte(out), runErr }
	return m
}

// FR-001: one text attribute, machine_id, on the host template.
func TestManifest(t *testing.T) {
	m := machineid.New()
	if m.Name() != "machine-id" {
		t.Fatalf("name %q", m.Name())
	}
	mf := m.Manifest()
	if err := manifest.Validate([]manifest.Manifest{mf}); err != nil {
		t.Fatal(err)
	}
	if len(mf.Attributes) != 1 || mf.Attributes[0].Key != "id" || mf.Attributes[0].Slug != "machine_id" || mf.Attributes[0].Kind != manifest.KindText || mf.Attributes[0].Template != "" {
		t.Fatalf("manifest: %+v", mf)
	}
}

// FR-007: derivation is a keyed hash: fixed vector, fixed length, not the raw value.
func TestDerive(t *testing.T) {
	got := machineid.Derive(rawLinux)
	if len(got) != 64 || got == rawLinux || strings.ToLower(got) != got {
		t.Fatalf("derived %q", got)
	}
	if got != machineid.Derive(rawLinux) {
		t.Fatal("derivation must be stable")
	}
	if machineid.Derive("other") == got {
		t.Fatal("different inputs must differ")
	}
}

// Golden vector pinned on 2026-09-22: HMAC-SHA256(key "omnistat/host-identity/v1").
// Changing the derivation is a breaking change (ADR-0004).
func TestDerive_Golden(t *testing.T) {
	if got, want := machineid.Derive(rawLinux), "f20a986a219366019ad3c8284a520267e327619d4d403db096b0ff52b66614e0"; got != want {
		t.Fatalf("derivation changed: got %s want %s", got, want)
	}
	if machineid.Derive(rawLinux+"\n") != machineid.Derive(rawLinux) {
		t.Fatal("raw id must be trimmed before hashing")
	}
}

// FR-004/007: Linux precedence and derivation; FR-005: static wins.
func TestDiscover_Linux(t *testing.T) {
	ctx := context.Background()
	fsys := fstest.MapFS{
		"etc/machine-id":          {Data: []byte(rawLinux + "\n")},
		"var/lib/dbus/machine-id": {Data: []byte("dbusdbusdbusdbusdbusdbusdbusdbus\n")},
	}
	id, err := mod("linux", fsys, "", nil).Discover(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if id.Source != machineid.SourceLinux || id.Value != machineid.Derive(rawLinux) {
		t.Fatalf("identity: %+v", id)
	}

	// fallback to D-Bus file
	delete(fsys, "etc/machine-id")
	id, err = mod("linux", fsys, "", nil).Discover(ctx, "")
	if err != nil || id.Value != machineid.Derive("dbusdbusdbusdbusdbusdbusdbusdbus") {
		t.Fatalf("dbus fallback: %+v %v", id, err)
	}

	// static wins and is published verbatim (trimmed)
	id, err = mod("linux", fsys, "", nil).Discover(ctx, "  rack7-node3  ")
	if err != nil || id.Source != machineid.SourceStatic || id.Value != "rack7-node3" {
		t.Fatalf("static: %+v %v", id, err)
	}
}

// FR-004: empty/all-zero → absent; FR-006: absent → ErrNoIdentity with guidance.
func TestDiscover_Absent(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		fsys fstest.MapFS
	}{
		{"no files", fstest.MapFS{}},
		{"empty file", fstest.MapFS{"etc/machine-id": {Data: []byte("\n")}}},
		{"all zero", fstest.MapFS{"etc/machine-id": {Data: []byte("00000000000000000000000000000000\n")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mod("linux", tt.fsys, "", nil).Discover(ctx, "")
			if !errors.Is(err, machineid.ErrNoIdentity) {
				t.Fatalf("want ErrNoIdentity, got %v", err)
			}
			for _, want := range []string{"/etc/machine-id", "identity.static", "OMNISTAT_IDENTITY"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q: %v", want, err)
				}
			}
		})
	}
}

// FR-004 macOS: ioreg output parsing; FR-006 when it fails.
func TestDiscover_Darwin(t *testing.T) {
	ctx := context.Background()
	out := `+-o MacBookPro18,3  <class IOPlatformExpertDevice, id 0x100000114, registered, matched, active, busy 0 (12 ms), retain 39>
    {
      "IOPlatformSerialNumber" = "XXXXXXXXXX"
      "IOPlatformUUID" = "` + rawDarwin + `"
      "board-id" = <"Mac-XXXXXXXX">
    }
`
	id, err := mod("darwin", fstest.MapFS{}, out, nil).Discover(ctx, "")
	if err != nil || id.Source != machineid.SourceDarwin || id.Value != machineid.Derive(rawDarwin) {
		t.Fatalf("darwin: %+v %v", id, err)
	}
	if _, err := mod("darwin", fstest.MapFS{}, "garbage", nil).Discover(ctx, ""); !errors.Is(err, machineid.ErrNoIdentity) {
		t.Fatalf("unparseable: %v", err)
	}
	if _, err := mod("darwin", fstest.MapFS{}, "", errors.New("exec: not found")).Discover(ctx, ""); !errors.Is(err, machineid.ErrNoIdentity) {
		t.Fatalf("runner failure: %v", err)
	}
	if _, err := mod("darwin", fstest.MapFS{}, strings.Replace(out, rawDarwin, "00000000-0000-0000-0000-000000000000", 1), nil).Discover(ctx, ""); !errors.Is(err, machineid.ErrNoIdentity) {
		t.Fatalf("all-zero uuid: %v", err)
	}
}

// Spec 006 FR-002/003: Windows reads the machine GUID and derives it like the
// other sources; empty, all-zero or unreadable → ErrNoIdentity (002 FR-006).
func TestDiscover_Windows(t *testing.T) {
	ctx := context.Background()
	win := func(guid string, err error) *machineid.Module {
		m := mod("windows", fstest.MapFS{}, "", nil)
		m.MachineGUID = func() (string, error) { return guid, err }
		return m
	}
	id, err := win("  "+rawWindows+"\r\n", nil).Discover(ctx, "")
	if err != nil || id.Source != machineid.SourceWindows || machineid.SourceWindows != "windows-machine-guid" {
		t.Fatalf("windows: %+v %v", id, err)
	}
	if id.Value != machineid.Derive(rawWindows) || len(id.Value) != 64 {
		t.Fatalf("must be derived exactly like the other sources (ADR-0004): %+v", id)
	}

	id, err = win(rawWindows, nil).Discover(ctx, "rack7-node3")
	if err != nil || id.Source != machineid.SourceStatic || id.Value != "rack7-node3" {
		t.Fatalf("static wins (002 FR-005): %+v %v", id, err)
	}

	for name, m := range map[string]*machineid.Module{
		"empty":      win("", nil),
		"all zero":   win("00000000-0000-0000-0000-000000000000", nil),
		"unreadable": win("", errors.New("registry: access denied")),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := m.Discover(ctx, "")
			if !errors.Is(err, machineid.ErrNoIdentity) {
				t.Fatalf("want ErrNoIdentity, got %v", err)
			}
			for _, want := range []string{"MachineGuid", "identity.static", "OMNISTAT_IDENTITY"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q: %v", want, err)
				}
			}
		})
	}
}

// The raw GUID must never surface, not even in the absent-identity error.
func TestDiscover_WindowsNeverShowsRaw(t *testing.T) {
	m := mod("windows", fstest.MapFS{}, "", nil)
	m.MachineGUID = func() (string, error) { return rawWindows, nil }
	id, err := m.Discover(context.Background(), "")
	if err != nil || strings.Contains(id.Value, rawWindows) {
		t.Fatalf("raw GUID leaked: %+v %v", id, err)
	}
}

func TestDiscover_UnsupportedOS(t *testing.T) {
	_, err := mod("plan9", fstest.MapFS{}, "", nil).Discover(context.Background(), "")
	if !errors.Is(err, machineid.ErrNoIdentity) || !strings.Contains(err.Error(), "plan9") {
		t.Fatalf("%v", err)
	}
}

// FR-008: static validation — empty, whitespace, too long.
func TestValidateStatic(t *testing.T) {
	if err := machineid.ValidateStatic("ok-1"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 129)} {
		if err := machineid.ValidateStatic(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
	if err := machineid.ValidateStatic(strings.Repeat("x", 128)); err != nil {
		t.Fatalf("128 chars must be fine: %v", err)
	}
}

// Spec 003 FR-005: the identity module produces no periodic values.
func TestNotAProvider(t *testing.T) {
	if _, ok := module.ProviderOf(machineid.New()); ok {
		t.Fatal("machine-id must not implement module.Provider")
	}
}
