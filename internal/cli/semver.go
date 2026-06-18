package cli

import (
	"strconv"
	"strings"
)

// compareVersions compares two "vX.Y.Z" version strings.
// It returns (cmp, ok): cmp is -1, 0, or 1 (a<b, a==b, a>b) and ok reports
// whether both strings parsed as dotted numeric versions. Pre-release and build
// suffixes (e.g. "-rc1", "+meta") are ignored for the comparison. When either
// value is not parseable (a branch name, "latest", a custom image tag, …) ok is
// false and callers should skip version-dependent guards rather than block.
func compareVersions(a, b string) (cmp int, ok bool) {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		switch {
		case pa[i] < pb[i]:
			return -1, true
		case pa[i] > pb[i]:
			return 1, true
		}
	}
	return 0, true
}

// parseVersion parses a "vX.Y.Z" string into its three numeric components.
// A leading "v" is optional; any "-"/"+" suffix is stripped before parsing.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Drop pre-release / build metadata.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
