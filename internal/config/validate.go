package config

import (
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/distribution/reference"
	"github.com/portainer/kubesolo/internal/config/cpumanager"
	"github.com/portainer/kubesolo/internal/runtime/cri"
	"github.com/portainer/kubesolo/types"
)

// ipv6MinimumMTU is the smallest MTU IPv6 permits. Below it, pod traffic cannot
// be fragmented correctly.
const ipv6MinimumMTU = 1280

// maxUnixSocketPath is the longest path a unix socket may have on Linux:
// sun_path is 108 bytes including its terminator. Exceeding it makes bind fail
// with "invalid argument", which says nothing about the cause — hence the check
// here, where the path can be named. macOS allows only 104, but KubeSolo runs on
// Linux and the difference only affects development.
const maxUnixSocketPath = 107

// Host carries what the validator cannot discover for itself. Passing these in
// rather than reading runtime.NumCPU and runtime.GOARCH directly keeps Validate
// free of side effects, and lets architecture-specific rules be tested on any
// machine.
type Host struct {
	NumCPU int
	GOARCH string

	// ContainerMode is what host detection found. It is only consulted when the
	// configuration does not state one explicitly.
	ContainerMode bool
}

// ResolveContainerMode reports whether KubeSolo runs in container mode: the
// configured value if it states one, otherwise what detection found.
func ResolveContainerMode(cfg *types.Config, detected bool) bool {
	if cfg.Runtime.ContainerMode != nil {
		return *cfg.Runtime.ContainerMode
	}
	return detected
}

// Validate checks a configuration and normalises it in place — expanding the
// Portainer image reference, filling in the CPU reservation the static policy
// requires, and disabling d2k where it cannot run.
//
// It has no side effects beyond cfg and never exits. Startup treats a returned
// error as fatal; the config API turns it into a rejected request. A validator
// that called log.Fatal would take the cluster down over a bad API payload.
// bootstrapTokenPattern is the token form the API server's bootstrap
// authenticator accepts, from k8s.io/cluster-bootstrap.
var bootstrapTokenPattern = regexp.MustCompile(`^[a-z0-9]{6}\.[a-z0-9]{16}$`)

func Validate(cfg *types.Config, host Host) ([]Warning, error) {
	var warnings []Warning

	// d2k first: the load balancer check below depends on whether this
	// architecture left it enabled.
	if cfg.D2K.Enabled && (host.GOARCH == "arm" || host.GOARCH == "riscv64") {
		warnings = append(warnings, Warning{
			Field:   "d2k.enabled",
			Message: fmt.Sprintf("d2k is not supported on %s, disabling it", host.GOARCH),
		})
		cfg.D2K.Enabled = false
	}

	if cfg.D2K.Enabled && !cfg.Network.LoadBalancer.Enabled {
		return warnings, fmt.Errorf("d2k.enabled requires network.loadBalancer.enabled: the d2k Service endpoint is populated by the LoadBalancer webhook")
	}

	if ResolveContainerMode(cfg, host.ContainerMode) && cfg.Kubernetes.Kubelet.CPUManager.Policy == types.CPUManagerPolicyStatic {
		return warnings, fmt.Errorf("kubernetes.kubelet.cpuManager.policy=%s is not supported in container mode: exclusive cores are bounded by the container's own cpuset, which KubeSolo does not control", types.CPUManagerPolicyStatic)
	}

	image, err := NormaliseImageRef(cfg.Portainer.Image)
	if err != nil {
		return warnings, fmt.Errorf("portainer.image: %w", err)
	}
	cfg.Portainer.Image = image

	if _, err := cri.Resolve(cfg.Runtime.Endpoint); err != nil {
		return warnings, fmt.Errorf("runtime.endpoint: %w", err)
	}

	// The API server parses the token into the Secret name bootstrap-token-<id>
	// and matches it against token-id/token-secret, so a token in any other shape
	// authenticates nothing. The failure is a 401 at the kubelet with no
	// indication that the token was the problem, so it is rejected here instead.
	if token := cfg.Kubernetes.BootstrapToken; token != "" && !bootstrapTokenPattern.MatchString(token) {
		return warnings, fmt.Errorf("kubernetes.bootstrapToken must look like %q (six lowercase alphanumerics, a dot, then sixteen)", "abcdef.0123456789abcdef")
	}

	// A malformed endpoint reaches the API server as an --etcd-servers value it
	// cannot dial, and the only symptom is the API server failing to start with a
	// storage error that does not name the configuration.
	for _, endpoint := range cfg.Storage.Etcd.Endpoints {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return warnings, fmt.Errorf("storage.etcd.endpoints: %q is not a URL of the form https://host:2379", endpoint)
		}
	}

	// etcd client authentication needs both halves, the same way a CA does.
	if (cfg.Storage.Etcd.CertFile == "") != (cfg.Storage.Etcd.KeyFile == "") {
		return warnings, fmt.Errorf("storage.etcd.certFile and storage.etcd.keyFile must be set together: they are one client credential")
	}

	// A supplied CA is only usable as a pair: KubeSolo signs every leaf
	// certificate with it, so a cert without its key leaves the control plane
	// unable to issue anything, and a key without its cert leaves it with no
	// trust anchor to publish.
	if (cfg.PKI.CACert == "") != (cfg.PKI.CAKey == "") {
		return warnings, fmt.Errorf("pki.caCert and pki.caKey must be set together: KubeSolo signs with this CA, so one without the other is unusable")
	}

	// Relative paths would resolve against KubeSolo's working directory, which is
	// whatever started it — a service manager, a shell, a container entrypoint.
	for path, field := range map[string]string{
		cfg.PKI.CACert:            "pki.caCert",
		cfg.PKI.CAKey:             "pki.caKey",
		cfg.Storage.Etcd.CAFile:   "storage.etcd.caFile",
		cfg.Storage.Etcd.CertFile: "storage.etcd.certFile",
		cfg.Storage.Etcd.KeyFile:  "storage.etcd.keyFile",
	} {
		if path != "" && !filepath.IsAbs(path) {
			return warnings, fmt.Errorf("%s must be an absolute path, got %q", field, path)
		}
	}

	// cpumanager.Parse is the single validator for these four settings: it checks
	// resource names and quantities, the supported policy option keys, and the
	// cpuset against the host. It takes the flag string form, so the map-typed
	// settings are joined back up rather than reimplementing any of that here.
	//
	// Its messages name flags rather than config paths, which reads oddly for a
	// config file; that is cosmetic and left alone rather than forking a second
	// copy of the validation.
	cpuManager, systemReserved, err := cpumanager.Parse(
		cfg.Kubernetes.Kubelet.CPUManager.Policy,
		joinKeyValues(cfg.Kubernetes.Kubelet.CPUManager.PolicyOptions),
		cfg.Kubernetes.Kubelet.CPUManager.ReservedCPUs,
		joinKeyValues(cfg.Kubernetes.Kubelet.SystemReserved),
		host.NumCPU,
	)
	if err != nil {
		return warnings, fmt.Errorf("kubernetes.kubelet: %w", err)
	}
	cfg.Kubernetes.Kubelet.CPUManager = cpuManager
	cfg.Kubernetes.Kubelet.SystemReserved = systemReserved

	if cfg.API.Enabled && len(cfg.API.SocketPath) > maxUnixSocketPath {
		return warnings, fmt.Errorf("api.socketPath is %d characters, which exceeds the %d-byte limit for a unix socket; shorten it or shorten path, from which it is derived",
			len(cfg.API.SocketPath), maxUnixSocketPath)
	}

	if cfg.Network.MTU > 0 && cfg.Network.MTU < ipv6MinimumMTU && !cfg.Network.DisableIPv6 {
		warnings = append(warnings, Warning{
			Field:   "network.mtu",
			Message: fmt.Sprintf("%d is below the IPv6 minimum of %d; IPv6 pod traffic may fail to fragment correctly", cfg.Network.MTU, ipv6MinimumMTU),
		})
	}

	return warnings, nil
}

// NormaliseImageRef expands a short image reference such as
// portainerci/agent:develop into a fully qualified one
// (docker.io/portainerci/agent:develop). The containerd client, unlike the
// Docker CLI, applies no Docker Hub defaults and would otherwise treat the first
// component as a registry host and fail to resolve it.
func NormaliseImageRef(image string) (string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %q: %v", image, err)
	}

	return reference.TagNameOnly(named).String(), nil
}

// joinKeyValues renders a map back into the comma-separated key=value form the
// flags use. Keys are sorted so the result is stable, which matters because it
// appears in error messages.
func joinKeyValues(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		pairs = append(pairs, k+"="+m[k])
	}
	return strings.Join(pairs, ",")
}
