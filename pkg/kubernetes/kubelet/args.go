package kubelet

import (
	"github.com/spf13/cobra"
)

func (s *service) configureKubeletArgs(command *cobra.Command) {
	command.SetArgs([]string{
		"--config", s.kubeletConfigFile,
		"--hostname-override", s.nodeName,
		"--root-dir", s.kubeletDir,
		"--kubeconfig", s.kubeletKubeConfigFile,
		"--v", "0",
	})
}
