package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"v1.2.3", [3]int{1, 2, 3}, true},
		{"1.2.3", [3]int{1, 2, 3}, true},
		{"  v1.1.7  ", [3]int{1, 1, 7}, true}, // surrounding whitespace trimmed
		{"v1.2.3-rc1", [3]int{1, 2, 3}, true}, // pre-release suffix stripped
		{"v1.2.3+build5", [3]int{1, 2, 3}, true},
		{"v10.20.30", [3]int{10, 20, 30}, true},
		{"latest", [3]int{}, false},
		{"develop", [3]int{}, false},
		{"v1.2", [3]int{}, false},     // too few components
		{"v1.2.3.4", [3]int{}, false}, // too many components
		{"v1.x.3", [3]int{}, false},   // non-numeric component
		{"", [3]int{}, false},
	}
	for _, c := range cases {
		got, ok := parseVersion(c.in)
		assert.Equalf(t, c.ok, ok, "parseVersion(%q) ok", c.in)
		if c.ok {
			assert.Equalf(t, c.want, got, "parseVersion(%q) value", c.in)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		cmp  int
		ok   bool
	}{
		{"v1.2.3", "v1.2.3", 0, true},
		{"v1.2.3", "v1.2.4", -1, true},
		{"v1.2.4", "v1.2.3", 1, true},
		{"v1.3.0", "v1.2.9", 1, true},
		{"v2.0.0", "v1.9.9", 1, true},
		{"v1.2.3-rc1", "v1.2.3", 0, true}, // suffixes ignored on both sides
		{"v1.1.7", "v1.1.7+meta", 0, true},
		{"latest", "v1.2.3", 0, false},  // unparseable -> ok=false
		{"v1.2.3", "develop", 0, false}, // unparseable -> ok=false
	}
	for _, c := range cases {
		cmp, ok := compareVersions(c.a, c.b)
		assert.Equalf(t, c.ok, ok, "compareVersions(%q,%q) ok", c.a, c.b)
		if c.ok {
			assert.Equalf(t, c.cmp, cmp, "compareVersions(%q,%q) cmp", c.a, c.b)
		}
	}
}
