package service

import "testing"

func TestInstanceName(t *testing.T) {
	cases := map[string]struct {
		name string
		ok   bool
	}{
		"kubesolo":           {"kubesolo", true},
		"kubesolo-dev":       {"dev", true},
		"kubesolo-":          {"", false},
		"kubesolo2":          {"", false},
		"rq-demo-control-pl": {"", false},
	}
	for cname, want := range cases {
		name, ok := instanceName(cname)
		if name != want.name || ok != want.ok {
			t.Errorf("%s: %q, %v; want %q, %v", cname, name, ok, want.name, want.ok)
		}
		if ok && ContainerNameFor(name) != cname {
			t.Errorf("%s: not the inverse of ContainerNameFor", cname)
		}
	}
}
