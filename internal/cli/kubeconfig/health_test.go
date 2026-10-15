package kubeconfig

import "testing"

func TestNodesReady(t *testing.T) {
	cases := []struct {
		name, body string
		want       bool
	}{
		{"ready", `{"items":[{"status":{"conditions":[{"type":"MemoryPressure","status":"False"},{"type":"Ready","status":"True"}]}}]}`, true},
		{"not ready", `{"items":[{"status":{"conditions":[{"type":"Ready","status":"False"}]}}]}`, false},
		{"no node yet", `{"items":[]}`, false},
		{"garbage", `not json`, false},
	}
	for _, c := range cases {
		if got := nodesReady([]byte(c.body)); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNodeNameAndLeaseRenewal(t *testing.T) {
	nodes := `{"items":[{"metadata":{"name":"kubesolo-dev"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`
	if got := nodeName([]byte(nodes)); got != "kubesolo-dev" {
		t.Errorf("nodeName = %q", got)
	}
	lease := `{"spec":{"holderIdentity":"kubesolo-dev","renewTime":"2026-10-15T03:04:05.123456Z"}}`
	if got := renewTime([]byte(lease)); got != "2026-10-15T03:04:05.123456Z" {
		t.Errorf("renewTime = %q", got)
	}
	if renewTime([]byte(`{"spec":{}}`)) != "" {
		t.Error("a lease never renewed has no renew time")
	}
}
