package controller

import (
	"strconv"

	"github.com/portainer/kubesolo/internal/logging"
	"github.com/spf13/cobra"
)

func (s *service) configureControllerManagerFlags(command *cobra.Command) {
	flags := command.Flags()

	// controller manager settings
	_ = flags.Set("allocate-node-cidrs", "true")
	_ = flags.Set("cluster-cidr", s.podCIDR)
	// The node's pod range is kept out of the service range, as the API server has it.
	_ = flags.Set("service-cluster-ip-range", s.serviceCIDR)
	_ = flags.Set("secure-port", strconv.Itoa(s.port))
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

	// logging - write through the kubesolo logger
	_ = flags.Set("logging-format", logging.K8sLogFormat)
}
