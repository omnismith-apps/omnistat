// Package upgrade is what `omnistat upgrade` does before it hands over to the
// new binary (spec 009): find the release, download and verify it, stage it
// where it can run, and run it. Deciding whether to upgrade and talking to the
// operator is the CLI's; the service backends say where the service is.
package upgrade

import (
	"regexp"
	"strconv"
	"strings"
)

// Version is a release version: SemVer 2.0 core and pre-release, no build
// metadata (release tags carry none).
type Version struct {
	Major, Minor, Patch int
	Pre                 string // "rc.1" for v0.5.0-rc.1; empty for a release
}

var (
	semverRE = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	// describeRE is what `git describe` appends past a tag: a development
	// build, not a release (FR-005).
	describeRE = regexp.MustCompile(`(^|-)\d+-g[0-9a-f]{4,}(-dirty)?$|(^|-)dirty$`)
)

// ParseVersion parses "v1.2.3" or "1.2.3-rc.1". ok is false for anything that
// is not a release version, a development build ("dev", a `git describe`
// string) included (FR-005).
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	m := semverRE.FindStringSubmatch(s)
	if m == nil || describeRE.MatchString(m[4]) {
		return Version{}, false
	}
	var v Version
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	v.Pre = m[4]
	return v, true
}

// ParseVersionOutput reads what `omnistat version` prints ("omnistat v0.3.0").
func ParseVersionOutput(out string) (Version, bool) {
	f := strings.Fields(out)
	if len(f) != 2 || f[0] != "omnistat" {
		return Version{}, false
	}
	return ParseVersion(f[1])
}

// String is the release tag: "v1.2.3" or "v1.2.3-rc.1".
func (v Version) String() string {
	return "v" + v.Number()
}

// Number is the version without the leading "v", as archive names spell it.
func (v Version) Number() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare returns -1, 0 or +1 as a is older than, equal to or newer than b, by
// SemVer precedence: a pre-release is older than its release, and pre-release
// identifiers compare numerically when both are numbers (FR-007).
func Compare(a, b Version) int {
	for _, d := range [3][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if c := cmpInt(d[0], d[1]); c != 0 {
			return c
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	as, bs := strings.Split(a.Pre, "."), strings.Split(b.Pre, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := comparePre(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(as), len(bs))
}

func comparePre(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return cmpInt(an, bn)
	case aerr == nil:
		return -1 // numeric identifiers are older than alphanumeric ones
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
