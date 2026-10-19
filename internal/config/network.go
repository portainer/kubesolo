package config

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"

	"github.com/portainer/kubesolo/types"
)

// clusterDNSOffset is the address in the service CIDR the CoreDNS Service gets
// when kubernetes.clusterDNS is left empty: the tenth, as kubeadm and k3s have
// it (10.43.0.10 in the default 10.43.0.0/16).
const clusterDNSOffset = 10

// PortUse is one port KubeSolo listens on, and what for.
type PortUse struct {
	Port int
	Name string

	// ConfigPath is the setting that chooses the port, for messages.
	ConfigPath string
}

// HostPorts returns the TCP ports KubeSolo listens on on the host, as cfg sets
// them: the ones a conflict with another process makes KubeSolo fail on. pprof
// is among them only when it is enabled, the metrics endpoint likewise.
//
// kube-proxy's metrics endpoint is not: KubeSolo disables it. CoreDNS's ports
// are not either: they are its pod's own, not the host's.
func HostPorts(cfg *types.Config) []PortUse {
	ports := []PortUse{
		{cfg.Storage.Kine.Port, "Kine (etcd replacement)", "storage.kine.port"},
		{cfg.Kubernetes.APIServer.Port, "Kubernetes API server", "kubernetes.apiServer.port"},
		{cfg.Kubernetes.ControllerManager.Port, "Kubernetes controller manager", "kubernetes.controllerManager.port"},
		{cfg.Kubernetes.Kubelet.Port, "kubelet", "kubernetes.kubelet.port"},
		{cfg.Kubernetes.Kubelet.HealthzPort, "kubelet health", "kubernetes.kubelet.healthzPort"},
		{cfg.Kubernetes.KubeProxy.HealthzPort, "kube-proxy health", "kubernetes.kubeProxy.healthzPort"},
		{cfg.Kubernetes.Webhook.Port, "Webhook server", "kubernetes.webhook.port"},
	}
	if cfg.Logging.Pprof {
		ports = append(ports, PortUse{cfg.Logging.PprofPort, "pprof HTTP server", "logging.pprofPort"})
	}
	if cfg.Metrics.Enabled {
		if _, port, err := net.SplitHostPort(cfg.Metrics.BindAddress); err == nil {
			if n, err := strconv.Atoi(port); err == nil {
				ports = append(ports, PortUse{n, "metrics endpoint", "metrics.bindAddress"})
			}
		}
	}
	return ports
}

// parseIPv4CIDR parses an IPv4 CIDR, returning its network.
func parseIPv4CIDR(cidr string) (*net.IPNet, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("%q is not a CIDR", cidr)
	}
	if network.IP.To4() == nil {
		return nil, fmt.Errorf("%q is not an IPv4 CIDR: KubeSolo's pod and service networks are IPv4", cidr)
	}
	return network, nil
}

// nthAddress returns the address n places into an IPv4 network: 0 is the
// network address itself. It reports false when the network has no such
// address.
func nthAddress(network *net.IPNet, n int) (net.IP, bool) {
	ones, bits := network.Mask.Size()
	if n < 0 || uint64(n) >= uint64(1)<<uint(bits-ones) {
		return nil, false
	}
	ip := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(ip, binary.BigEndian.Uint32(network.IP.To4())+uint32(n))
	return ip, true
}

// KubernetesServiceIP is the cluster IP of the kubernetes Service: the first
// address of the service CIDR, which the API server allocates to it. The API
// server certificate carries it.
func KubernetesServiceIP(serviceCIDR string) (string, error) {
	network, err := parseIPv4CIDR(serviceCIDR)
	if err != nil {
		return "", err
	}
	ip, ok := nthAddress(network, 1)
	if !ok {
		return "", fmt.Errorf("%s has no address for the kubernetes Service", serviceCIDR)
	}
	return ip.String(), nil
}

// ClusterDNS is the cluster IP of the CoreDNS Service: the one configured, or
// the tenth address of the service CIDR.
func ClusterDNS(cfg *types.Config) (string, error) {
	if cfg.Kubernetes.ClusterDNS != "" {
		return cfg.Kubernetes.ClusterDNS, nil
	}
	network, err := parseIPv4CIDR(cfg.Kubernetes.ServiceCIDR)
	if err != nil {
		return "", err
	}
	ip, ok := nthAddress(network, clusterDNSOffset)
	if !ok {
		return "", fmt.Errorf("%s is too small for the default cluster DNS address (its %dth); set kubernetes.clusterDNS", cfg.Kubernetes.ServiceCIDR, clusterDNSOffset)
	}
	return ip.String(), nil
}
