package upgrade

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a KubeSolo release version: vMAJOR.MINOR.PATCH with an optional
// pre-release suffix, as the release tags are written.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
	raw                 string
}

var versionPattern = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?$`)

// ParseVersion parses a release tag such as "v1.2.1" or "v1.3.0-rc.1".
//
// It is strict on purpose: the version names a download URL and an archive, and
// is compared against what the new binary reports about itself. A build from an
// untagged commit ("v1.2.1-5-gabc123-dirty", "dev") parses only if it happens to
// fit the pattern, which callers treat as an ordinary pre-release.
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a release version (expected vMAJOR.MINOR.PATCH, e.g. v1.2.1)", s)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return Version{Major: major, Minor: minor, Patch: patch, Pre: m[4], raw: s}, nil
}

func (v Version) String() string { return v.raw }

// Compare returns -1, 0 or 1 as v is older than, the same as, or newer than o,
// following semver precedence: a pre-release sorts before its release.
func (v Version) Compare(o Version) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	return comparePre(v.Pre, o.Pre)
}

func comparePre(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		an, aErr := strconv.Atoi(as[i])
		bn, bErr := strconv.Atoi(bs[i])
		switch {
		case aErr == nil && bErr == nil:
			if an < bn {
				return -1
			}
			return 1
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		case as[i] < bs[i]:
			return -1
		default:
			return 1
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

// versionInOutput finds the version the kubesolo binary reports for itself in
// the output of `kubesolo --version`, which is a zerolog line such as
// {"level":"info","version":"v1.2.1","time":"...","message":"kubesolo version"}.
var versionInOutput = regexp.MustCompile(`"version":"([^"]+)"`)

// ReportedVersion extracts the version from `kubesolo --version` output.
func ReportedVersion(output string) (string, bool) {
	m := versionInOutput.FindStringSubmatch(output)
	if m == nil {
		return "", false
	}
	return m[1], true
}
