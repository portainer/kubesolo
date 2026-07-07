package controller

import (
	"github.com/portainer/kubesolo/types"
	"github.com/spf13/cobra"
)

func (s *service) configureControllerManagerFlags(command *cobra.Command) {
	flags := command.Flags()

	// controller manager settings
	_ = flags.Set("allocate-node-cidrs", "true")
	_ = flags.Set("cluster-cidr", types.DefaultPodCIDR)
	_ = flags.Set("service-account-private-key-file", s.serviceAccountKeyFile)
	_ = flags.Set("kubeconfig", s.adminKubeconfigFile)
	_ = flags.Set("authentication-kubeconfig", s.adminKubeconfigFile)
	_ = flags.Set("authorization-kubeconfig", s.adminKubeconfigFile)
	_ = flags.Set("root-ca-file", s.caFile)
	_ = flags.Set("requestheader-client-ca-file", s.caFile)
	_ = flags.Set("tls-cert-file", s.controllerManagerCertFile)
	_ = flags.Set("tls-private-key-file", s.controllerManagerKeyFile)
	_ = flags.Set("leader-elect", "false")
	_ = flags.Set("use-service-account-credentials", "true")
}
