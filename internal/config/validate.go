package config

import (
	"fmt"
	"maps"
	"net"
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

// The sizes the Kubernetes components accept for the cluster networks. The
// controller manager carves a /24 out of the pod CIDR for the node, so the pod
// CIDR must hold one. The API server refuses an IPv4 service CIDR larger than a
// /12 or smaller than a /29, and the default cluster DNS address, the tenth,
// needs at least a /28.
const (
	nodePodCIDRPrefix      = 24
	largestServiceCIDR     = 12
	smallestServiceCIDR    = 29
	smallestDNSServiceCIDR = 28
)

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

	// Both are used verbatim as addresses: in the API server's advertise
	// address, the kubelet's node IP, the kubeconfig and Service EXTERNAL-IPs.
	// A typo there does not fail until KubeSolo is already down.
	for field, ip := range map[string]string{"network.nodeIP": cfg.Network.NodeIP, "network.loadBalancer.ip": cfg.Network.LoadBalancer.IP} {
		if ip != "" && net.ParseIP(ip) == nil {
			return warnings, fmt.Errorf("%s: %q is not an IP address", field, ip)
		}
	}

	if err := validatePorts(cfg); err != nil {
		return warnings, err
	}

	if err := validateNetworks(cfg); err != nil {
		return warnings, err
	}

	// A detected node IP is checked as KubeSolo starts, once it is known.
	if w, ok := NodeIPWarning(cfg, cfg.Network.NodeIP); ok {
		warnings = append(warnings, w)
	}

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

// validatePorts checks every port is one, and that no two of the ports KubeSolo
// listens on are the same: two of its own components on one port fail as a port
// held by another process does, but only once KubeSolo is already starting.
func validatePorts(cfg *types.Config) error {
	ports := append(HostPorts(cfg), PortUse{cfg.D2K.Port, "d2k Service", "d2k.port"})
	for _, p := range ports {
		if p.Port < 1 || p.Port > 65535 {
			return fmt.Errorf("%s: %d is not a port (1-65535)", p.ConfigPath, p.Port)
		}
	}
	seen := map[int]string{}
	for _, p := range HostPorts(cfg) {
		if other, ok := seen[p.Port]; ok {
			return fmt.Errorf("%s and %s are both %d: each needs a port of its own", other, p.ConfigPath, p.Port)
		}
		seen[p.Port] = p.ConfigPath
	}
	// d2k's is a Service port at the LoadBalancer IP — the node IP unless
	// network.loadBalancer.ip says otherwise — which kube-proxy takes over there:
	// on one of KubeSolo's own ports it would take that port's traffic.
	if cfg.D2K.Enabled {
		if other, ok := seen[cfg.D2K.Port]; ok {
			return fmt.Errorf("d2k.port and %s are both %d: the d2k LoadBalancer Service would take the node's traffic to that port", other, cfg.D2K.Port)
		}
	}
	return nil
}

// validateNetworks checks the pod and service CIDRs and the cluster DNS address.
// A CIDR must be written as its network address (10.42.0.0/16, not
// 10.42.1.0/16).
func validateNetworks(cfg *types.Config) error {
	podNet, err := parseIPv4CIDR(cfg.Kubernetes.PodCIDR)
	if err != nil {
		return fmt.Errorf("kubernetes.podCIDR: %w", err)
	}
	if ones, _ := podNet.Mask.Size(); ones > nodePodCIDRPrefix {
		return fmt.Errorf("kubernetes.podCIDR: %s is smaller than the /%d the controller manager allocates to the node", cfg.Kubernetes.PodCIDR, nodePodCIDRPrefix)
	}

	serviceNet, err := parseIPv4CIDR(cfg.Kubernetes.ServiceCIDR)
	if err != nil {
		return fmt.Errorf("kubernetes.serviceCIDR: %w", err)
	}
	serviceOnes, serviceBits := serviceNet.Mask.Size()
	if serviceOnes < largestServiceCIDR {
		return fmt.Errorf("kubernetes.serviceCIDR: %s is larger than the /%d the API server accepts", cfg.Kubernetes.ServiceCIDR, largestServiceCIDR)
	}
	if serviceOnes > smallestServiceCIDR {
		return fmt.Errorf("kubernetes.serviceCIDR: %s is smaller than the /%d the API server accepts", cfg.Kubernetes.ServiceCIDR, smallestServiceCIDR)
	}

	if podNet.Contains(serviceNet.IP) || serviceNet.Contains(podNet.IP) {
		return fmt.Errorf("kubernetes.podCIDR %s and kubernetes.serviceCIDR %s overlap", cfg.Kubernetes.PodCIDR, cfg.Kubernetes.ServiceCIDR)
	}

	// Refused rather than normalised: the config API, a restart and the record
	// of the networks compare them as written.
	for field, n := range map[string]struct {
		cidr    string
		network *net.IPNet
	}{"kubernetes.podCIDR": {cfg.Kubernetes.PodCIDR, podNet}, "kubernetes.serviceCIDR": {cfg.Kubernetes.ServiceCIDR, serviceNet}} {
		if n.network.String() != n.cidr {
			return fmt.Errorf("%s: %s is not a network address; use %s", field, n.cidr, n.network)
		}
	}

	if cfg.Kubernetes.ClusterDNS == "" {
		if serviceOnes > smallestDNSServiceCIDR {
			return fmt.Errorf("kubernetes.serviceCIDR: %s has no room for the default cluster DNS address (its %dth); set kubernetes.clusterDNS", cfg.Kubernetes.ServiceCIDR, clusterDNSOffset)
		}
		return nil
	}
	dns := net.ParseIP(cfg.Kubernetes.ClusterDNS)
	if dns == nil || dns.To4() == nil {
		return fmt.Errorf("kubernetes.clusterDNS: %q is not an IPv4 address", cfg.Kubernetes.ClusterDNS)
	}
	if !serviceNet.Contains(dns) {
		return fmt.Errorf("kubernetes.clusterDNS: %s is not in kubernetes.serviceCIDR %s", cfg.Kubernetes.ClusterDNS, cfg.Kubernetes.ServiceCIDR)
	}
	// Neither the network address nor the broadcast address is ever allocated, and
	// the first address is the kubernetes Service's.
	kubernetesIP, _ := nthAddress(serviceNet, 1)
	broadcast, _ := nthAddress(serviceNet, 1<<(serviceBits-serviceOnes)-1)
	if dns.Equal(serviceNet.IP) || dns.Equal(broadcast) || dns.Equal(kubernetesIP) {
		return fmt.Errorf("kubernetes.clusterDNS: %s is the service CIDR's network or broadcast address, or the kubernetes Service's (%s)", cfg.Kubernetes.ClusterDNS, kubernetesIP)
	}
	return nil
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
