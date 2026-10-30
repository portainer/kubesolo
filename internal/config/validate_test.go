package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/types"
)

func testHost() Host { return Host{NumCPU: 4, GOARCH: "amd64"} }

// TestValidateNeverExits is the property the config API depends on. A validator
// that terminated the process would turn a bad request body into a dead
// cluster, so the package is inspected for such calls rather than trusted not
// to make them.
//
// The check parses each file rather than grepping it: prose about these calls
// is legitimate, only calling them is not.
func TestValidateNeverExits(t *testing.T) {
	forbidden := map[string]bool{
		"log.Fatal": true, "log.Fatalf": true, "log.Panic": true,
		"os.Exit": true, "panic": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var callee string
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				callee = fn.Name
			case *ast.SelectorExpr:
				if x, ok := fn.X.(*ast.Ident); ok {
					callee = x.Name + "." + fn.Sel.Name
				}
			}
			if forbidden[callee] {
				t.Errorf("%s calls %s at %s; the config package must return errors, not terminate",
					name, callee, fset.Position(call.Pos()))
			}
			return true
		})
	}
}

func TestValidateDefaultsAreValid(t *testing.T) {
	warnings, err := Validate(Defaults(), testHost())
	if err != nil {
		t.Fatalf("the default configuration must validate: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("the default configuration should be warning-free, got %v", warnings)
	}
}

func TestD2KDisabledOnUnsupportedArch(t *testing.T) {
	for _, arch := range []string{"arm", "riscv64"} {
		t.Run(arch, func(t *testing.T) {
			cfg := Defaults()
			cfg.D2K.Enabled = true

			host := testHost()
			host.GOARCH = arch
			warnings, err := Validate(cfg, host)
			if err != nil {
				t.Fatalf("unsupported architecture should warn, not fail: %v", err)
			}
			if cfg.D2K.Enabled {
				t.Error("d2k should have been disabled")
			}
			if len(warnings) == 0 {
				t.Error("expected a warning explaining why d2k was disabled")
			}
		})
	}
}

// TestD2KOnUnsupportedArchIgnoresLoadBalancer pins the ordering of the two d2k
// rules: the architecture check disables d2k first, so the load balancer
// requirement no longer applies. Reversing them would fail a configuration that
// is in fact fine.
func TestD2KOnUnsupportedArchIgnoresLoadBalancer(t *testing.T) {
	cfg := Defaults()
	cfg.D2K.Enabled = true
	cfg.Network.LoadBalancer.Enabled = false

	host := testHost()
	host.GOARCH = "arm"
	if _, err := Validate(cfg, host); err != nil {
		t.Fatalf("d2k was disabled by architecture, so the load balancer rule should not fire: %v", err)
	}
}

func TestD2KRequiresLoadBalancer(t *testing.T) {
	cfg := Defaults()
	cfg.D2K.Enabled = true
	cfg.Network.LoadBalancer.Enabled = false

	_, err := Validate(cfg, testHost())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "loadBalancer") {
		t.Errorf("error should name the setting to change, got: %v", err)
	}
}

func TestStaticCPUPolicyRejectedInContainerMode(t *testing.T) {
	yes := true
	for _, tc := range []struct {
		name      string
		explicit  *bool
		detected  bool
		wantError bool
	}{
		{"explicitly in container mode", &yes, false, true},
		{"detected container mode", nil, true, true},
		{"not in container mode", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Kubernetes.Kubelet.CPUManager.Policy = types.CPUManagerPolicyStatic
			cfg.Runtime.ContainerMode = tc.explicit

			host := testHost()
			host.ContainerMode = tc.detected
			_, err := Validate(cfg, host)
			if tc.wantError && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestExplicitContainerModeOverridesDetection covers the reason the setting is a
// pointer: an explicit false must beat detection, which a plain bool could not
// express.
func TestExplicitContainerModeOverridesDetection(t *testing.T) {
	no := false
	cfg := Defaults()
	cfg.Runtime.ContainerMode = &no
	if ResolveContainerMode(cfg, true) {
		t.Error("an explicit false must override detection")
	}
}

func TestPortainerImageNormalised(t *testing.T) {
	cfg := Defaults()
	cfg.Portainer.Image = "portainerci/agent:develop"

	if _, err := Validate(cfg, testHost()); err != nil {
		t.Fatal(err)
	}
	if want := "docker.io/portainerci/agent:develop"; cfg.Portainer.Image != want {
		t.Errorf("image = %q, want %q", cfg.Portainer.Image, want)
	}
}

func TestInvalidValuesRejected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.Config)
		field  string
	}{
		{"bad image reference", func(c *types.Config) { c.Portainer.Image = "NOT A REF" }, "portainer.image"},
		{"relative runtime endpoint", func(c *types.Config) { c.Runtime.Endpoint = "run/crio.sock" }, "runtime.endpoint"},
		{"unknown cpu manager policy", func(c *types.Config) {
			c.Kubernetes.Kubelet.CPUManager.Policy = "dynamic"
		}, "kubernetes.kubelet"},
		{"unsupported reserved resource", func(c *types.Config) {
			c.Kubernetes.Kubelet.SystemReserved = map[string]string{"gpu": "1"}
		}, "kubernetes.kubelet"},
		{"unparseable quantity", func(c *types.Config) {
			c.Kubernetes.Kubelet.SystemReserved = map[string]string{"memory": "banana"}
		}, "kubernetes.kubelet"},
		{"unsupported policy option", func(c *types.Config) {
			c.Kubernetes.Kubelet.CPUManager.Policy = types.CPUManagerPolicyStatic
			c.Kubernetes.Kubelet.CPUManager.PolicyOptions = map[string]string{"make-it-fast": "true"}
		}, "kubernetes.kubelet"},
		{"reserved cpu that does not exist", func(c *types.Config) {
			c.Kubernetes.Kubelet.CPUManager.Policy = types.CPUManagerPolicyStatic
			c.Kubernetes.Kubelet.CPUManager.ReservedCPUs = "99"
		}, "kubernetes.kubelet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.mutate(cfg)

			_, err := Validate(cfg, testHost())
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error should be attributed to %s, got: %v", tc.field, err)
			}
		})
	}
}

// TestStaticPolicyFillsReservedCPUs pins the normalisation: the static policy
// refuses to start without a reservation, so one is supplied.
func TestStaticPolicyFillsReservedCPUs(t *testing.T) {
	cfg := Defaults()
	cfg.Kubernetes.Kubelet.CPUManager.Policy = types.CPUManagerPolicyStatic

	if _, err := Validate(cfg, testHost()); err != nil {
		t.Fatal(err)
	}
	if cfg.Kubernetes.Kubelet.CPUManager.ReservedCPUs == "" {
		t.Error("the static policy requires a non-empty CPU reservation")
	}
}

func TestLowMTUWarnsOnlyWithIPv6(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mtu         int
		disableIPv6 bool
		wantWarning bool
	}{
		{"below the IPv6 minimum", 1000, false, true},
		{"below it with IPv6 disabled", 1000, true, false},
		{"at the IPv6 minimum", 1280, false, false},
		{"auto-detect", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Network.MTU = tc.mtu
			cfg.Network.DisableIPv6 = tc.disableIPv6

			warnings, err := Validate(cfg, testHost())
			if err != nil {
				t.Fatal(err)
			}
			got := false
			for _, w := range warnings {
				if w.Field == "network.mtu" {
					got = true
				}
			}
			if got != tc.wantWarning {
				t.Errorf("mtu warning = %v, want %v (warnings: %v)", got, tc.wantWarning, warnings)
			}
		})
	}
}

// A node IP the host already uses inside a cluster network is the mistake the
// configurable networks are most likely to be met with; it is warned about,
// not refused.
func TestNodeIPInAClusterNetworkWarns(t *testing.T) {
	for _, tc := range []struct {
		name, nodeIP, wantIn string
	}{
		{"in the pod network", "10.42.3.4", "kubernetes.podCIDR"},
		{"in the service network", "10.43.0.200", "kubernetes.serviceCIDR"},
		{"in neither", "192.168.1.10", ""},
		{"detected at start, not here", "", ""},
		{"IPv6", "fd00::1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Network.NodeIP = tc.nodeIP

			warnings, err := Validate(cfg, testHost())
			if err != nil {
				t.Fatal(err)
			}
			var got string
			for _, w := range warnings {
				if strings.Contains(w.Message, "holds the node IP") {
					got = w.Field
				}
			}
			if got != tc.wantIn {
				t.Errorf("node IP warning on %q, want %q (warnings: %v)", got, tc.wantIn, warnings)
			}
		})
	}
}

// TestValidateIsIdempotent guards the normalisation: the API validates on every
// write, so running it over an already-normalised config must not drift.
func TestValidateIsIdempotent(t *testing.T) {
	cfg := Defaults()
	cfg.Kubernetes.Kubelet.CPUManager.Policy = types.CPUManagerPolicyStatic
	cfg.Portainer.Image = "portainer/agent:lts"

	if _, err := Validate(cfg, testHost()); err != nil {
		t.Fatal(err)
	}
	first := *cfg
	if _, err := Validate(cfg, testHost()); err != nil {
		t.Fatal(err)
	}
	if first.Portainer.Image != cfg.Portainer.Image ||
		first.Kubernetes.Kubelet.CPUManager.ReservedCPUs != cfg.Kubernetes.Kubelet.CPUManager.ReservedCPUs {
		t.Errorf("validation is not idempotent:\nfirst:  %+v\nsecond: %+v", first, *cfg)
	}
}

// TestLongSocketPathRejected covers a failure that is otherwise reported as
// "bind: invalid argument" from deep inside the network stack, with nothing to
// indicate the path length is the problem.
func TestLongSocketPathRejected(t *testing.T) {
	cfg := Defaults()
	cfg.API.Enabled = true
	cfg.API.SocketPath = "/" + strings.Repeat("a", maxUnixSocketPath) + "/config.sock"

	_, err := Validate(cfg, testHost())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "api.socketPath") {
		t.Errorf("error should name the setting, got: %v", err)
	}
}

// TestLongSocketPathIgnoredWhenAPIDisabled — the path is never bound, so its
// length cannot matter.
func TestLongSocketPathIgnoredWhenAPIDisabled(t *testing.T) {
	cfg := Defaults()
	cfg.API.Enabled = false
	cfg.API.SocketPath = "/" + strings.Repeat("a", maxUnixSocketPath) + "/config.sock"

	if _, err := Validate(cfg, testHost()); err != nil {
		t.Fatalf("a disabled API must not be validated for path length: %v", err)
	}
}

// TestDefaultSocketPathFitsComfortably guards the default against drifting past
// the limit as the default base path changes.
func TestDefaultSocketPathFitsComfortably(t *testing.T) {
	cfg := Defaults()
	derive(cfg)

	if got := len(cfg.API.SocketPath); got > maxUnixSocketPath {
		t.Errorf("default socket path is %d characters, over the %d limit", got, maxUnixSocketPath)
	}
}

func TestValidateRejectsMalformedAddresses(t *testing.T) {
	for _, c := range []struct {
		set  func(*types.Config)
		want string
	}{
		{func(c *types.Config) { c.Network.NodeIP = "not-an-ip" }, "network.nodeIP"},
		{func(c *types.Config) { c.Network.NodeIP = "10.0.0.300" }, "network.nodeIP"},
		{func(c *types.Config) { c.Network.LoadBalancer.IP = "lb.example.com" }, "network.loadBalancer.ip"},
	} {
		cfg := Defaults()
		c.set(cfg)
		if _, err := Validate(cfg, Host{NumCPU: 4, GOARCH: "amd64"}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("err = %v, want one naming %s", err, c.want)
		}
	}
	for _, ip := range []string{"10.0.0.5", "fd00::5"} {
		cfg := Defaults()
		cfg.Network.NodeIP, cfg.Network.LoadBalancer.IP = ip, ip
		if _, err := Validate(cfg, Host{NumCPU: 4, GOARCH: "amd64"}); err != nil {
			t.Errorf("%s: %v", ip, err)
		}
	}
}

func TestInvalidPortsRejected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.Config)
		field  string
	}{
		{"port zero", func(c *types.Config) { c.Kubernetes.APIServer.Port = 0 }, "kubernetes.apiServer.port"},
		{"port above 65535", func(c *types.Config) { c.Storage.Kine.Port = 65536 }, "storage.kine.port"},
		{"d2k port out of range", func(c *types.Config) { c.D2K.Port = -1 }, "d2k.port"},
		{"two components on one port", func(c *types.Config) { c.Kubernetes.Webhook.Port = c.Kubernetes.APIServer.Port }, "kubernetes.webhook.port"},
		{"pprof on the kubelet's port", func(c *types.Config) {
			c.Logging.Pprof = true
			c.Logging.PprofPort = c.Kubernetes.Kubelet.Port
		}, "logging.pprofPort"},
		{"d2k on the API server's port", func(c *types.Config) {
			c.D2K.Enabled = true
			c.D2K.Port = c.Kubernetes.APIServer.Port
		}, "d2k.port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.mutate(cfg)

			_, err := Validate(cfg, testHost())
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error should be attributed to %s, got: %v", tc.field, err)
			}
		})
	}
}

// A port taken only when its user is enabled: pprof's and d2k's are free to
// coincide with another while they are off.
func TestDisabledPortsMayCoincide(t *testing.T) {
	cfg := Defaults()
	cfg.Logging.PprofPort = cfg.Kubernetes.Kubelet.Port
	cfg.D2K.Port = cfg.Kubernetes.APIServer.Port
	if _, err := Validate(cfg, testHost()); err != nil {
		t.Fatalf("disabled pprof and d2k should not conflict: %v", err)
	}
}

func TestInvalidNetworksRejected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.Config)
		field  string
	}{
		{"pod CIDR not a CIDR", func(c *types.Config) { c.Kubernetes.PodCIDR = "10.42.0.0" }, "kubernetes.podCIDR"},
		{"pod CIDR IPv6", func(c *types.Config) { c.Kubernetes.PodCIDR = "fd00::/48" }, "kubernetes.podCIDR"},
		{"pod CIDR smaller than the node's /24", func(c *types.Config) { c.Kubernetes.PodCIDR = "10.42.0.0/25" }, "kubernetes.podCIDR"},
		{"service CIDR larger than a /12", func(c *types.Config) { c.Kubernetes.ServiceCIDR = "10.0.0.0/11" }, "kubernetes.serviceCIDR"},
		{"service CIDR smaller than a /29", func(c *types.Config) {
			c.Kubernetes.ServiceCIDR = "10.43.0.0/30"
			c.Kubernetes.ClusterDNS = "10.43.0.2"
		}, "kubernetes.serviceCIDR"},
		{"service CIDR with no room for the default DNS address", func(c *types.Config) { c.Kubernetes.ServiceCIDR = "10.43.0.0/29" }, "kubernetes.serviceCIDR"},
		{"overlapping networks", func(c *types.Config) { c.Kubernetes.ServiceCIDR = "10.42.128.0/24" }, "kubernetes.podCIDR"},
		{"DNS not an address", func(c *types.Config) { c.Kubernetes.ClusterDNS = "dns" }, "kubernetes.clusterDNS"},
		{"DNS outside the service CIDR", func(c *types.Config) { c.Kubernetes.ClusterDNS = "10.44.0.10" }, "kubernetes.clusterDNS"},
		{"DNS on the network address", func(c *types.Config) { c.Kubernetes.ClusterDNS = "10.43.0.0" }, "kubernetes.clusterDNS"},
		{"DNS on the kubernetes Service's address", func(c *types.Config) { c.Kubernetes.ClusterDNS = "10.43.0.1" }, "kubernetes.clusterDNS"},
		{"DNS on the broadcast address", func(c *types.Config) {
			c.Kubernetes.ServiceCIDR = "10.43.0.0/24"
			c.Kubernetes.ClusterDNS = "10.43.0.255"
		}, "kubernetes.clusterDNS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.mutate(cfg)

			_, err := Validate(cfg, testHost())
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error should be attributed to %s, got: %v", tc.field, err)
			}
		})
	}
}

// The networks are normalised to their network address, which is what the
// components are given, and the edge sizes are accepted.
func TestNetworksNormalisedAndAccepted(t *testing.T) {
	cfg := Defaults()
	cfg.Kubernetes.PodCIDR = "10.42.1.0/16"
	cfg.Kubernetes.ServiceCIDR = "10.96.0.7/12"
	if _, err := Validate(cfg, testHost()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.Kubernetes.PodCIDR != "10.42.0.0/16" || cfg.Kubernetes.ServiceCIDR != "10.96.0.0/12" {
		t.Errorf("networks = %s, %s; want 10.42.0.0/16, 10.96.0.0/12", cfg.Kubernetes.PodCIDR, cfg.Kubernetes.ServiceCIDR)
	}

	cfg = Defaults()
	cfg.Kubernetes.PodCIDR = "10.42.7.0/24"
	cfg.Kubernetes.ServiceCIDR = "10.43.0.0/28"
	if _, err := Validate(cfg, testHost()); err != nil {
		t.Fatalf("a /24 pod CIDR and a /28 service CIDR should be accepted: %v", err)
	}
}

func TestDerivedServiceAddresses(t *testing.T) {
	ip, err := KubernetesServiceIP("10.96.0.0/12")
	if err != nil || ip != "10.96.0.1" {
		t.Errorf("KubernetesServiceIP = %q, %v; want 10.96.0.1", ip, err)
	}

	cfg := Defaults()
	if dns, err := ClusterDNS(cfg); err != nil || dns != types.DefaultCoreDNSIP {
		t.Errorf("ClusterDNS (default) = %q, %v; want %s", dns, err, types.DefaultCoreDNSIP)
	}
	cfg.Kubernetes.ServiceCIDR = "10.96.0.0/12"
	if dns, err := ClusterDNS(cfg); err != nil || dns != "10.96.0.10" {
		t.Errorf("ClusterDNS (derived) = %q, %v; want 10.96.0.10", dns, err)
	}
	cfg.Kubernetes.ClusterDNS = "10.96.0.53"
	if dns, err := ClusterDNS(cfg); err != nil || dns != "10.96.0.53" {
		t.Errorf("ClusterDNS (set) = %q, %v; want 10.96.0.53", dns, err)
	}
}
