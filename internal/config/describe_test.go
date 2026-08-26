package config

import (
	"encoding/json"
	"testing"

	"github.com/portainer/kubesolo/types"
)

func TestMergePatchFor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		mutate func(*types.Config)
		want   string
	}{
		{
			name:   "string",
			path:   "network.nodeIP",
			mutate: func(c *types.Config) { c.Network.NodeIP = "10.0.0.5" },
			want:   `{"network":{"nodeIP":"10.0.0.5"}}`,
		},
		{
			name:   "integer stays a number",
			path:   "network.mtu",
			mutate: func(c *types.Config) { c.Network.MTU = 1400 },
			want:   `{"network":{"mtu":1400}}`,
		},
		{
			name:   "boolean stays a boolean",
			path:   "logging.debug",
			mutate: func(c *types.Config) { c.Logging.Debug = true },
			want:   `{"logging":{"debug":true}}`,
		},
		{
			name:   "three levels deep",
			path:   "network.loadBalancer.enabled",
			mutate: func(c *types.Config) { c.Network.LoadBalancer.Enabled = false },
			want:   `{"network":{"loadBalancer":{"enabled":false}}}`,
		},
		{
			name:   "four levels deep",
			path:   "kubernetes.kubelet.cpuManager.policy",
			mutate: func(c *types.Config) { c.Kubernetes.Kubelet.CPUManager.Policy = "static" },
			want:   `{"kubernetes":{"kubelet":{"cpuManager":{"policy":"static"}}}}`,
		},
		{
			name:   "list stays a list",
			path:   "kubernetes.apiServer.extraSANs",
			mutate: func(c *types.Config) { c.Kubernetes.APIServer.ExtraSANs = []string{"a.local", "b.local"} },
			want:   `{"kubernetes":{"apiServer":{"extraSANs":["a.local","b.local"]}}}`,
		},
		{
			name:   "map stays a map",
			path:   "kubernetes.kubelet.systemReserved",
			mutate: func(c *types.Config) { c.Kubernetes.Kubelet.SystemReserved = map[string]string{"cpu": "1"} },
			want:   `{"kubernetes":{"kubelet":{"systemReserved":{"cpu":"1"}}}}`,
		},
		{
			// omitempty drops the key, and null is the right patch for it: under
			// RFC 7386 it removes the setting, restoring its default.
			name:   "cleared list becomes null",
			path:   "kubernetes.apiServer.extraSANs",
			mutate: func(c *types.Config) { c.Kubernetes.APIServer.ExtraSANs = nil },
			want:   `{"kubernetes":{"apiServer":{"extraSANs":null}}}`,
		},
		{
			name:   "tri-state pointer set to false",
			path:   "runtime.containerMode",
			mutate: func(c *types.Config) { no := false; c.Runtime.ContainerMode = &no },
			want:   `{"runtime":{"containerMode":false}}`,
		},
		{
			name:   "unset tri-state pointer becomes null",
			path:   "runtime.containerMode",
			mutate: func(c *types.Config) { c.Runtime.ContainerMode = nil },
			want:   `{"runtime":{"containerMode":null}}`,
		},
		{
			name:   "top level",
			path:   "path",
			mutate: func(c *types.Config) { c.Path = "/opt/kubesolo" },
			want:   `{"path":"/opt/kubesolo"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.mutate(cfg)

			got, err := MergePatchFor(tc.path, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestMergePatchForEverySetting checks the builder against the whole registry,
// so a new setting cannot be added that `config set` silently cannot reach.
func TestMergePatchForEverySetting(t *testing.T) {
	for _, f := range Registry() {
		t.Run(f.ConfigPath, func(t *testing.T) {
			patch, err := MergePatchFor(f.ConfigPath, Defaults())
			if err != nil {
				t.Fatalf("cannot build a patch for %s: %v", f.ConfigPath, err)
			}

			// The patch must be a valid document on its own, and applying it to
			// the defaults must leave them unchanged — it carries their values.
			var probe map[string]any
			if err := json.Unmarshal(patch, &probe); err != nil {
				t.Fatalf("patch is not valid JSON: %v", err)
			}

			applied := Defaults()
			if err := json.Unmarshal(patch, applied); err != nil {
				t.Fatalf("patch does not apply: %v", err)
			}
			if !equalSetting(f, applied, Defaults()) {
				t.Errorf("a patch built from the defaults changed %s", f.ConfigPath)
			}
		})
	}
}

func equalSetting(f Field, a, b *types.Config) bool {
	x, _ := json.Marshal(f.Get(a))
	y, _ := json.Marshal(f.Get(b))
	return string(x) == string(y)
}
