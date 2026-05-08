package kubelet

import (
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
		args = append(args, "--node-ip", s.nodeIP)
	}
	command.SetArgs(args)
}
