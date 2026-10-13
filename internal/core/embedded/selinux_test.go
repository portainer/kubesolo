package embedded

import "testing"

func TestModuleListed(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"name only", "container\nkubesolo\nvirt\n", true},
		{"name and version", "container\t2.229.0\nkubesolo\t1.0\n", true},
		{"prefix of another module", "kubesolo_bpf\nkubesolo-extra\n", false},
		{"absent", "container\nvirt\n", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := moduleListed([]byte(c.out), SELinuxPolicyModule); got != c.want {
			t.Errorf("%s: moduleListed() = %v, want %v", c.name, got, c.want)
		}
	}
}
