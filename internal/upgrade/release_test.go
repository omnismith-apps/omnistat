package upgrade_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/http/httpproxy"

	"github.com/omnismith-apps/omnistat/internal/upgrade"
	"github.com/omnismith-apps/omnistat/internal/upgrade/upgradetest"
)

// FR-015: the default location, a mirror, and the HTTPS-only policy.
func TestReleasesURL(t *testing.T) {
	for _, tc := range []struct{ in, want, err string }{
		{"", upgrade.DefaultReleasesURL, ""},
		{"https://mirror.example/omnistat/", "https://mirror.example/omnistat", ""},
		{"http://127.0.0.1:8080/rel", "http://127.0.0.1:8080/rel", ""},
		{"http://localhost:8080", "http://localhost:8080", ""},
		{"http://[::1]:8080", "http://[::1]:8080", ""},
		{"http://mirror.example/omnistat", "", "must be an https"},
		{"http://10.0.0.5/omnistat", "", "must be an https"},
		{"ftp://mirror.example", "", "must be an https"},
		{"file:///srv/omnistat", "", "not a usable URL"},
		{"https://user:pw@mirror.example", "", "not a usable URL"},
		{"mirror.example", "", "not a usable URL"},
	} {
		got, err := upgrade.ReleasesURL(func(string) string { return tc.in })
		if got != tc.want || (tc.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("ReleasesURL(%q) = %q, %v; want %q, %q", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestAssetName(t *testing.T) {
	v := mustVersion(t, "v0.5.0-rc.1")
	if got := upgrade.AssetName(v, "linux", "arm64"); got != "omnistat_0.5.0-rc.1_linux_arm64.tar.gz" {
		t.Error(got)
	}
	if got := upgrade.AssetName(v, "windows", "amd64"); got != "omnistat_0.5.0-rc.1_windows_amd64.zip" {
		t.Error(got)
	}
}

func client() *http.Client { return upgrade.NewClient(httpproxy.Config{}) }

// FR-006, FR-008: latest and pinned releases, the asset for this platform.
func TestResolve(t *testing.T) {
	srv := upgradetest.New()
	defer srv.Close()
	srv.Add("v0.3.0", []byte("bin 0.3.0"), "linux/amd64", "linux/arm64", "windows/amd64")
	srv.Add("v0.4.0", []byte("bin 0.4.0"), "linux/amd64", "windows/amd64")
	srv.Add("v0.5.0-rc.1", []byte("bin rc"), "linux/amd64")
	srv.Latest = "v0.4.0"
	ctx := context.Background()

	rel, err := upgrade.Resolve(ctx, client(), srv.URL, "", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version.String() != "v0.4.0" || rel.Asset != "omnistat_0.4.0_linux_amd64.tar.gz" ||
		rel.ArchiveURL != srv.URL+"/download/v0.4.0/omnistat_0.4.0_linux_amd64.tar.gz" ||
		rel.SHA256 != upgradetest.Sum(upgradetest.Archive("linux", []byte("bin 0.4.0"))) {
		t.Fatalf("latest: %+v", rel)
	}
	if rel.ChecksumsURL != srv.URL+"/latest/download/checksums.txt" {
		t.Fatalf("latest read from %s", rel.ChecksumsURL)
	}

	for _, want := range []string{"v0.3.0", "0.3.0"} {
		rel, err = upgrade.Resolve(ctx, client(), srv.URL, want, "linux", "arm64")
		if err != nil || rel.Version.String() != "v0.3.0" || rel.Asset != "omnistat_0.3.0_linux_arm64.tar.gz" {
			t.Fatalf("pinned %s: %+v %v", want, rel, err)
		}
	}
	rel, err = upgrade.Resolve(ctx, client(), srv.URL, "v0.5.0-rc.1", "linux", "amd64")
	if err != nil || rel.Version.String() != "v0.5.0-rc.1" {
		t.Fatalf("pre-release: %+v %v", rel, err)
	}
	rel, err = upgrade.Resolve(ctx, client(), srv.URL, "", "windows", "amd64")
	if err != nil || rel.Asset != "omnistat_0.4.0_windows_amd64.zip" {
		t.Fatalf("windows: %+v %v", rel, err)
	}

	for _, tc := range []struct{ want, goos, goarch, err string }{
		{"v9.9.9", "linux", "amd64", "release v9.9.9 not found"},
		{"", "linux", "arm64", "the latest release has no archive for linux/arm64"},
		{"v0.4.0", "darwin", "arm64", "release v0.4.0 has no archive for darwin/arm64"},
		{"latest", "linux", "amd64", "not a release version"},
	} {
		if _, err := upgrade.Resolve(ctx, client(), srv.URL, tc.want, tc.goos, tc.goarch); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("Resolve(%q, %s/%s) = %v, want %q", tc.want, tc.goos, tc.goarch, err, tc.err)
		}
	}
	if srv.Downloaded() {
		t.Fatal("Resolve must read checksums only")
	}
}

// FR-009: a malformed checksums file is refused, naming the line.
func TestResolve_BadChecksums(t *testing.T) {
	good := strings.Repeat("a", 64)
	for body, want := range map[string]string{
		"not a checksum line\n": "line 1",
		good + "  omnistat_0.4.0_linux_amd64.tar.gz\n" + strings.Repeat("z", 64) + "  x\n":                                 "line 2: the hash is not hexadecimal",
		good + "  omnistat_0.4.0_linux_amd64.tar.gz\n" + strings.Repeat("b", 64) + "  omnistat_0.4.0_linux_amd64.tar.gz\n": "listed twice",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
		_, err := upgrade.Resolve(context.Background(), client(), srv.URL, "", "linux", "amd64")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", body, err, want)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer srv.Close()
	if _, err := upgrade.Resolve(context.Background(), client(), srv.URL, "", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("HTTP status not reported: %v", err)
	}
	if _, err := upgrade.Resolve(context.Background(), client(), srv.URL, "", "linux", "amd64"); errors.Is(err, upgrade.ErrNotFound) {
		t.Error("502 is not 404")
	}
}
