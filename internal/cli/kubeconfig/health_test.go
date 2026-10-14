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
