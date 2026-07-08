package kubelet

import (
	"net"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func (s *service) configureKubeletArgs(command *cobra.Command) {
	args := []string{
		"--config", s.kubeletConfigFile,
		"--hostname-override", s.nodeName,
		"--root-dir", s.kubeletDir,
		"--kubeconfig", s.kubeletKubeConfigFile,
	}
	// Register the node with the resolved node IP so the kubelet's InternalIP
	// matches the API server advertise address (and any --node-ip override).
	// Without this the kubelet auto-detects its InternalIP, which on multi-NIC
	// hosts can pick a different interface (e.g. a public IP) than the one
	// kubesolo advertises.
	ip := net.ParseIP(s.nodeIP)
	if ip != nil && !ip.IsLoopback() {
		args = append(args, "--node-ip", s.nodeIP)
	} else if s.disableIPv6 {
		log.Warn().Str("component", "kubelet").Msgf("--disable-ipv6 set but node IP %q is loopback or invalid; skipping --node-ip", s.nodeIP)
	}
	command.SetArgs(args)
}
