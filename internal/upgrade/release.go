package upgrade

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultReleasesURL is where releases are published (FR-015).
const DefaultReleasesURL = "https://github.com/omnismith-apps/omnistat/releases"

// EnvReleasesURL names a mirror with the same layout (FR-015).
const EnvReleasesURL = "OMNISTAT_RELEASES_URL"

// Download limits (FR-010).
const (
	checksumsLimit   = 1 << 20
	checksumsTimeout = time.Minute
)

// ErrInsecureURL: a releases location that is neither HTTPS nor loopback HTTP.
var ErrInsecureURL = errors.New(EnvReleasesURL + " must be an https:// URL (http:// only for localhost)")

// ReleasesURL is the releases location: the default, or the mirror named by
// OMNISTAT_RELEASES_URL, which must be HTTPS unless it is on this machine
// (FR-015). The result has no trailing slash.
func ReleasesURL(getenv func(string) string) (string, error) {
	raw := strings.TrimSpace(getenv(EnvReleasesURL))
	if raw == "" {
		return DefaultReleasesURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%s: not a usable URL: %q", EnvReleasesURL, raw)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && loopback(u.Hostname()):
	default:
		return "", fmt.Errorf("%w: %s", ErrInsecureURL, raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Release is the archive upgrade installs (FR-006, FR-008).
type Release struct {
	Version Version
	// Asset is the archive's file name, SHA256 its expected hash (hex).
	Asset  string
	SHA256 string
	// ArchiveURL is where the archive is downloaded from: always the tagged
	// location, so a release published meanwhile cannot swap it.
	ArchiveURL string
	// ChecksumsURL is the checksums file the hash came from.
	ChecksumsURL string
}

// AssetName is the archive the release pipeline publishes for goos/goarch
// (.goreleaser.yaml name_template; zip on Windows).
func AssetName(v Version, goos, goarch string) string {
	return "omnistat_" + v.Number() + "_" + goos + "_" + goarch + archiveExt(goos)
}

func archiveExt(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// Resolve finds the release to install: the latest one when want is empty
// (never a pre-release: the location's "latest" excludes them), else exactly
// want (FR-006). The version and the hash come from the release's
// checksums.txt, so no API is needed and a mirror is plain files.
func Resolve(ctx context.Context, c *http.Client, base, want, goos, goarch string) (Release, error) {
	var (
		pinned Version
		sumURL = base + "/latest/download/checksums.txt"
	)
	if want != "" {
		var ok bool
		if pinned, ok = ParseVersion(want); !ok {
			return Release{}, fmt.Errorf("--version %q is not a release version (vX.Y.Z or vX.Y.Z-rc.N)", want)
		}
		sumURL = base + "/download/" + pinned.String() + "/checksums.txt"
	}
	sums, err := fetchChecksums(ctx, c, sumURL)
	switch {
	case errors.Is(err, ErrNotFound) && want != "":
		return Release{}, fmt.Errorf("release %s not found: %w", pinned, err)
	case err != nil:
		return Release{}, fmt.Errorf("reading the release's checksums: %w", err)
	}

	suffix := "_" + goos + "_" + goarch + archiveExt(goos)
	var found []Release
	for name, sum := range sums {
		num, ok := strings.CutPrefix(name, "omnistat_")
		if !ok {
			continue
		}
		if num, ok = strings.CutSuffix(num, suffix); !ok {
			continue
		}
		v, ok := ParseVersion(num)
		if !ok || v.Number() != num || (want != "" && Compare(v, pinned) != 0) {
			continue
		}
		found = append(found, Release{Version: v, Asset: name, SHA256: sum, ChecksumsURL: sumURL,
			ArchiveURL: base + "/download/" + v.String() + "/" + name})
	}
	switch len(found) {
	case 0:
		what := "the latest release"
		if want != "" {
			what = "release " + pinned.String()
		}
		return Release{}, fmt.Errorf("%s has no archive for %s/%s (%s)", what, goos, goarch, sumURL)
	case 1:
		return found[0], nil
	}
	return Release{}, fmt.Errorf("%s lists more than one archive for %s/%s", sumURL, goos, goarch)
}

// fetchChecksums reads a GoReleaser checksums file: "<sha256>  <name>" lines.
func fetchChecksums(ctx context.Context, c *http.Client, sumURL string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, checksumsTimeout)
	defer cancel()
	body, err := get(ctx, c, sumURL, checksumsLimit)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	sums := map[string]string{}
	sc := bufio.NewScanner(body)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || len(f[0]) != 64 {
			return nil, fmt.Errorf("%s: line %d is not \"<sha256>  <file>\"", sumURL, n)
		}
		if _, err := hex.DecodeString(f[0]); err != nil {
			return nil, fmt.Errorf("%s: line %d: the hash is not hexadecimal", sumURL, n)
		}
		name := strings.TrimPrefix(f[1], "*") // sha256sum's binary-mode marker
		if prev, dup := sums[name]; dup && prev != strings.ToLower(f[0]) {
			return nil, fmt.Errorf("%s: %s is listed twice with different hashes", sumURL, name)
		}
		sums[name] = strings.ToLower(f[0])
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", sumURL, err)
	}
	return sums, nil
}
