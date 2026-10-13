package syspath

import "testing"

func TestWithSystemDirs(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"su without -", "/usr/bin:/bin", "/usr/bin:/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/sbin"},
		{"already complete", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		{"keeps custom dirs first", "/opt/tools:/usr/bin", "/opt/tools:/usr/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/sbin:/bin"},
		{"empty", "", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		{"drops empty entries", "/usr/bin::", "/usr/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/sbin:/bin"},
	}
	for _, c := range cases {
		if got := withSystemDirs(c.in); got != c.want {
			t.Errorf("%s: withSystemDirs(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
