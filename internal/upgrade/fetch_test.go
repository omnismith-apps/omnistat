package upgrade_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/upgrade"
	"github.com/omnismith-apps/omnistat/internal/upgrade/upgradetest"
)

// serveArchive serves data as the archive of a release whose checksum is sum.
func serveArchive(t *testing.T, name string, data []byte, sum string) upgrade.Release {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	t.Cleanup(srv.Close)
	return upgrade.Release{Asset: name, SHA256: sum, ArchiveURL: srv.URL + "/" + name}
}

// FR-009: the archive is verified, then only the binary is unpacked; the
// archive itself does not stay.
func TestFetch(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			data := upgradetest.Archive(goos, []byte("new omnistat"))
			rel := serveArchive(t, "omnistat_0.4.0_"+goos+"_amd64.x", data, upgradetest.Sum(data))
			dir := t.TempDir()
			bin, err := upgrade.Fetch(context.Background(), client(), rel, dir, goos)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(bin) != upgrade.BinaryName(goos) {
				t.Fatalf("binary at %s", bin)
			}
			got, _ := os.ReadFile(bin) //nolint:gosec // the test's own file
			if !bytes.Equal(got, []byte("new omnistat")) {
				t.Fatalf("unpacked %q", got)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("the staging directory holds %d entries, want only the binary", len(entries))
			}
			if fi, _ := os.Stat(bin); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
				t.Fatalf("mode %v", fi.Mode())
			}
		})
	}
}

// FR-009: nothing is unpacked from an archive that fails verification, and an
// archive without the binary at its top level is an error.
func TestFetch_Refused(t *testing.T) {
	good := upgradetest.Archive("linux", []byte("new omnistat"))
	nested := upgradetest.TarGz(upgradetest.File{Name: "dir/omnistat", Data: []byte("x")}, upgradetest.File{Name: "README.md", Data: []byte("r")})
	for _, tc := range []struct {
		name string
		data []byte
		sum  string
		err  error
		msg  string
	}{
		{name: "checksum mismatch", data: good, sum: upgradetest.Sum([]byte("other")), err: upgrade.ErrChecksum},
		{name: "no binary", data: nested, sum: upgradetest.Sum(nested), err: upgrade.ErrNoBinary},
		{name: "not an archive", data: []byte("<html>"), sum: upgradetest.Sum([]byte("<html>")), msg: "unpacking"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel := serveArchive(t, "omnistat_0.4.0_linux_amd64.tar.gz", tc.data, tc.sum)
			dir := t.TempDir()
			_, err := upgrade.Fetch(context.Background(), client(), rel, dir, "linux")
			if err == nil || (tc.err != nil && !errors.Is(err, tc.err)) || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("%v", err)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("left %d files behind", len(entries))
			}
		})
	}
}

// FR-011: a new private directory; leftovers of an interrupted upgrade go.
func TestStage(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, upgrade.StagePrefix+"123")
	if err := os.MkdirAll(filepath.Join(left, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "omnistat")
	if err := os.WriteFile(keep, []byte("installed"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := upgrade.Stage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Fatal("leftover not removed")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("the installed binary must stay")
	}
	fi, err := os.Stat(path)
	if err != nil || filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), upgrade.StagePrefix) {
		t.Fatalf("%s %v", path, err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", fi.Mode())
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cleanup must remove the staging directory")
	}
}
