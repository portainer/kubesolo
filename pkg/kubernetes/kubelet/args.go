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
	if s.disableIPv6 {
		ip := net.ParseIP(s.nodeIP)
		if ip != nil && !ip.IsLoopback() {
			args = append(args, "--node-ip", s.nodeIP)
		} else {
			log.Warn().Str("component", "kubelet").Msgf("--disable-ipv6 set but node IP %q is loopback or invalid; skipping --node-ip", s.nodeIP)
		}
	}
	command.SetArgs(args)
}
