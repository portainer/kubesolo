package config

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/portainer/kubesolo/internal/runtime/cri"
	"github.com/portainer/kubesolo/types"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata/embedded")

// goldenCase pairs a name with the configuration and host probe it exercises.
// Together the cases cover every axis BuildEmbedded branches on or interpolates.
//
// The configurations are built up from a bare types.Config rather than
// Defaults(), so each case varies exactly one thing and the golden files isolate
// the mapping under test from the default values, which Defaults() already pins.
type goldenCase struct {
	name  string
	cfg   *types.Config
	probe Probe
}

// cfgWith returns a bare config with the given adjustments applied.
func cfgWith(adjust ...func(*types.Config)) *types.Config {
	cfg := &types.Config{}
	for _, a := range adjust {
		a(cfg)
	}
	return cfg
}

func at(path string) func(*types.Config) {
	return func(c *types.Config) { c.Path = path }
}

func goldenCases() []goldenCase {
	const base = "/var/lib/kubesolo"

	return []goldenCase{
		{name: "zero", cfg: cfgWith()},
		{
			name: "defaults",
			cfg: cfgWith(at(base), func(c *types.Config) {
				c.Network.LoadBalancer.Enabled = true
				c.Portainer.Image = "docker.io/portainer/agent:lts"
				c.D2K.Namespace = "d2k"
				c.Kubernetes.Kubelet.CPUManager = types.CPUManagerConfig{Policy: "none"}
				c.Metrics.BindAddress = "127.0.0.1:9105"
			}),
			probe: Probe{NodeIP: "192.168.1.10", LoadBalancerIP: "192.168.1.10", MTU: 1500},
		},
		{name: "custom-base-path", cfg: cfgWith(at("/opt/kubesolo")), probe: Probe{NodeIP: "10.0.0.5"}},
		{name: "node-ip-pinned", cfg: cfgWith(at(base)), probe: Probe{NodeIP: "10.0.0.5", NodeIPPinned: true}},
		{name: "mtu-pinned", cfg: cfgWith(at(base)), probe: Probe{MTU: 1400, MTUPinned: true}},
		{name: "container-mode", cfg: cfgWith(at(base)), probe: Probe{ContainerMode: true}},
		{
			name: "external-runtime",
			cfg:  cfgWith(at(base)),
			probe: Probe{RuntimeEndpoint: cri.Endpoint{
				URL: "unix:///run/crio/crio.sock", SocketPath: "/run/crio/crio.sock", External: true,
			}},
		},
		{name: "embedded-runtime-explicit-zero", cfg: cfgWith(at(base)), probe: Probe{RuntimeEndpoint: cri.Endpoint{}}},
		{name: "extra-sans-empty", cfg: cfgWith(at(base), sans())},
		{name: "extra-sans-single", cfg: cfgWith(at(base), sans("kubesolo.local"))},
		{name: "extra-sans-multiple", cfg: cfgWith(at(base), sans("10.0.0.4", "kubesolo.local"))},
		{name: "load-balancer-off", cfg: cfgWith(at(base), func(c *types.Config) {
			c.Network.LoadBalancer.Enabled = false
		})},
		{
			name: "load-balancer-distinct-ip",
			cfg: cfgWith(at(base), func(c *types.Config) {
				c.Network.LoadBalancer.Enabled = true
			}),
			probe: Probe{NodeIP: "10.0.0.5", LoadBalancerIP: "10.0.0.9"},
		},
		{name: "portainer-edge-full", cfg: cfgWith(at(base), func(c *types.Config) {
			c.Portainer.EdgeID = "edge-id-123"
			c.Portainer.EdgeKey = "edge-key-456"
			c.Portainer.Image = "docker.io/portainerci/agent:develop"
		})},
		{name: "portainer-edge-id-only", cfg: cfgWith(at(base), func(c *types.Config) {
			c.Portainer.EdgeID = "edge-id-123"
		})},
		{name: "portainer-edge-key-only", cfg: cfgWith(at(base), func(c *types.Config) {
			c.Portainer.EdgeKey = "edge-key-456"
		})},
		{name: "disable-ipv6", cfg: cfgWith(at(base), func(c *types.Config) {
			c.Network.DisableIPv6 = true
		})},
		{name: "d2k-enabled", cfg: cfgWith(at(base), func(c *types.Config) {
			c.D2K.Enabled = true
			c.D2K.Namespace = "d2k"
		})},
		{name: "d2k-custom-namespace", cfg: cfgWith(at(base), func(c *types.Config) {
			c.D2K.Enabled = true
			c.D2K.Namespace = "workloads"
		})},
		{name: "cpu-manager-static", cfg: cfgWith(at(base), func(c *types.Config) {
			c.Kubernetes.Kubelet.CPUManager = types.CPUManagerConfig{
				Policy:        types.CPUManagerPolicyStatic,
				PolicyOptions: map[string]string{"full-pcpus-only": "true"},
				ReservedCPUs:  "0-1",
			}
			c.Kubernetes.Kubelet.SystemReserved = map[string]string{"cpu": "1", "memory": "500Mi"}
		})},
		{
			name: "node-name-explicit",
			cfg: cfgWith(at(base), func(c *types.Config) {
				c.Kubernetes.NodeName = "talos-cp-1"
			}),
			probe: Probe{Hostname: "kubesolo-container"},
		},
		{
			name: "node-name-normalised",
			cfg: cfgWith(at(base), func(c *types.Config) {
				c.Kubernetes.NodeName = "  Talos-CP-1  "
			}),
			probe: Probe{Hostname: "kubesolo-container"},
		},
		{
			name:  "node-name-falls-back-to-hostname",
			cfg:   cfgWith(at(base)),
			probe: Probe{Hostname: "edge-box-7"},
		},
		{name: "bootstrap-token", cfg: cfgWith(at(base), func(c *types.Config) {
			c.Kubernetes.BootstrapToken = "abcdef.0123456789abcdef"
		})},
		{name: "external-ca", cfg: cfgWith(at(base), func(c *types.Config) {
			c.PKI.CACert = "/etc/talos/pki/ca.crt"
			c.PKI.CAKey = "/etc/talos/pki/ca.key"
		})},
		{name: "metrics-enabled", cfg: cfgWith(at(base), func(c *types.Config) {
			c.Metrics = types.MetricsConfig{Enabled: true, BindAddress: "0.0.0.0:9105"}
		})},
	}
}

func sans(names ...string) func(*types.Config) {
	return func(c *types.Config) { c.Kubernetes.APIServer.ExtraSANs = names }
}

// TestBuildEmbeddedGolden pins the mapping from Input to types.Embedded. Any
// change to a derived path shows up as a golden diff rather than as a silent
// behaviour change at boot. Regenerate with: go test ./internal/config/ -update
func TestBuildEmbeddedGolden(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.MarshalIndent(BuildEmbedded(tc.cfg, tc.probe), "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')

			path := filepath.Join("testdata", "embedded", tc.name+".json")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("types.Embedded does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
			}
		})
	}
}
