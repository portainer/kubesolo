package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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

// TestValidateExternalCAPairing — a CA certificate without its key cannot sign,
// and a key without its certificate leaves nothing to publish as a trust anchor.
// Accepting either half would fail much later, as a TLS error naming some leaf.
func TestValidateExternalCAPairing(t *testing.T) {
	tests := []struct {
		name    string
		cert    string
		key     string
		wantErr string
	}{
		{name: "neither is the default"},
		{name: "both together", cert: "/etc/talos/ca.crt", key: "/etc/talos/ca.key"},
		{name: "cert without key", cert: "/etc/talos/ca.crt", wantErr: "must be set together"},
		{name: "key without cert", key: "/etc/talos/ca.key", wantErr: "must be set together"},
		{name: "relative cert", cert: "pki/ca.crt", key: "/etc/talos/ca.key", wantErr: "absolute path"},
		{name: "relative key", cert: "/etc/talos/ca.crt", key: "pki/ca.key", wantErr: "absolute path"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.PKI.CACert = test.cert
			cfg.PKI.CAKey = test.key

			_, err := Validate(cfg, testHost())
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", test.wantErr, err)
			}
		})
	}
}

// TestValidateBootstrapToken — the API server derives the Secret name from the
// token id, so a malformed token authenticates nothing and surfaces only as a
// 401 at the kubelet, which never names the token as the cause.
func TestValidateBootstrapToken(t *testing.T) {
	for _, token := range []string{"", "abcdef.0123456789abcdef", "07401b.f395accd246ae52d"} {
		cfg := Defaults()
		cfg.Kubernetes.BootstrapToken = token
		if _, err := Validate(cfg, testHost()); err != nil {
			t.Errorf("token %q: expected no error, got %v", token, err)
		}
	}

	invalid := []string{
		"abcdef",                    // no secret
		"abcdef.0123",               // secret too short
		"ABCDEF.0123456789abcdef",   // uppercase
		"abcde.0123456789abcdef",    // id too short
		"abcdef-0123456789abcdef",   // wrong separator
		"abcdef.0123456789abcdefff", // secret too long
	}
	for _, token := range invalid {
		cfg := Defaults()
		cfg.Kubernetes.BootstrapToken = token
		if _, err := Validate(cfg, testHost()); err == nil {
			t.Errorf("token %q: expected an error, got none", token)
		}
	}
}

// TestValidateBootstrapKubeconfig — the token and the file it would be read
// from cannot both be authoritative, and a relative path resolves against
// whatever directory KubeSolo happened to be started in.
func TestValidateBootstrapKubeconfig(t *testing.T) {
	cfg := Defaults()
	cfg.Kubernetes.BootstrapKubeconfig = "/etc/kubernetes/bootstrap-kubeconfig"
	if _, err := Validate(cfg, testHost()); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	cfg = Defaults()
	cfg.Kubernetes.BootstrapKubeconfig = "bootstrap-kubeconfig"
	if _, err := Validate(cfg, testHost()); err == nil {
		t.Error("expected a relative path to be rejected, got none")
	}

	cfg = Defaults()
	cfg.Kubernetes.BootstrapToken = "abcdef.0123456789abcdef"
	cfg.Kubernetes.BootstrapKubeconfig = "/etc/kubernetes/bootstrap-kubeconfig"
	if _, err := Validate(cfg, testHost()); err == nil {
		t.Error("expected setting both to be rejected, got none")
	}
}

// TestValidateEtcdEndpoints — a malformed endpoint reaches the API server as an
// --etcd-servers value it cannot dial, and the only symptom is the API server
// failing to start with a storage error that never names the configuration.
func TestValidateEtcdEndpoints(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []string
		cert, key string
		wantErr   string
	}{
		{name: "unset is the default"},
		{name: "https endpoint", endpoints: []string{"https://127.0.0.1:2379"}},
		{name: "several endpoints", endpoints: []string{"https://10.0.0.1:2379", "https://10.0.0.2:2379"}},
		{name: "http endpoint", endpoints: []string{"http://10.0.0.5:2379"}},
		{name: "ipv6 endpoint", endpoints: []string{"https://[::1]:2379"}},
		{name: "no scheme", endpoints: []string{"127.0.0.1:2379"}, wantErr: "is not a URL"},
		{name: "no host", endpoints: []string{"https://"}, wantErr: "is not a URL"},
		// url.Parse accepts both of these: only syntax is its concern.
		{name: "unsupported scheme", endpoints: []string{"ftp://host:2379"}, wantErr: "must use http or https"},
		{name: "no port", endpoints: []string{"https://host"}, wantErr: "must name a port"},
		{name: "cert without key", cert: "/etc/etcd/client.crt", wantErr: "must be set together"},
		{name: "key without cert", key: "/etc/etcd/client.key", wantErr: "must be set together"},
		{name: "relative cert", cert: "etcd/client.crt", key: "/etc/etcd/client.key", wantErr: "absolute path"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Storage.Etcd.Endpoints = test.endpoints
			cfg.Storage.Etcd.CertFile = test.cert
			cfg.Storage.Etcd.KeyFile = test.key

			_, err := Validate(cfg, testHost())
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", test.wantErr, err)
			}
		})
	}
}

// TestValidateExternalCALocation — the managed PKI directory is swept whenever
// leaf certificates are regenerated, so a CA placed there would be deleted by
// something as ordinary as the node IP changing.
func TestValidateExternalCALocation(t *testing.T) {
	cfg := Defaults()
	managed := filepath.Join(cfg.Path, "pki")

	cases := map[string]struct {
		dir     string
		wantErr bool
	}{
		"outside the managed tree":    {dir: "/system/secrets/kubernetes", wantErr: false},
		"in the preserved ca dir":     {dir: filepath.Join(managed, "ca"), wantErr: false},
		"in the swept apiserver dir":  {dir: filepath.Join(managed, "apiserver"), wantErr: true},
		"at the root of the pki tree": {dir: managed, wantErr: true},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Defaults()
			cfg.PKI.CACert = filepath.Join(test.dir, "ca.crt")
			cfg.PKI.CAKey = filepath.Join(test.dir, "ca.key")

			_, err := Validate(cfg, testHost())
			if test.wantErr && err == nil {
				t.Errorf("expected an error for %s, got none", test.dir)
			}
			if !test.wantErr && err != nil {
				t.Errorf("expected no error for %s, got %v", test.dir, err)
			}
		})
	}
}
