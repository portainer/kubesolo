package controller

import (
	"github.com/portainer/kubesolo/types"
	"github.com/spf13/cobra"
)

func (s *service) configureControllerManagerFlags(command *cobra.Command) {
	flags := command.Flags()
	_ = flags.Set("service-account-private-key-file", s.serviceAccountKeyFile)
	_ = flags.Set("kubeconfig", s.adminKubeconfigFile)
	_ = flags.Set("authentication-kubeconfig", s.adminKubeconfigFile)
	_ = flags.Set("authorization-kubeconfig", s.adminKubeconfigFile)
	_ = flags.Set("root-ca-file", s.caFile)
	_ = flags.Set("requestheader-client-ca-file", s.caFile)
	_ = flags.Set("tls-cert-file", s.controllerManagerCertFile)
	_ = flags.Set("tls-private-key-file", s.controllerManagerKeyFile)
	_ = flags.Set("leader-elect", "false")
	_ = flags.Set("controllers", "deployment,replicaset,service,serviceaccount,namespace,attachdetach,endpoint,daemonset,statefulset,root-ca-certificate-publisher-controller,serviceaccount-token-controller,node-ipam-controller,endpointslice-controller,garbage-collector-controller,ttl-after-finished-controller")
	_ = flags.Set("profiling", "false")
	_ = flags.Set("use-service-account-credentials", "true")
	_ = flags.Set("bind-address", "0.0.0.0")
	_ = flags.Set("secure-port", "10257")
	_ = flags.Set("allocate-node-cidrs", "true")
	_ = flags.Set("cluster-cidr", types.DefaultPodCIDR)
	_ = flags.Set("concurrent-deployment-syncs", "1")
	_ = flags.Set("concurrent-endpoint-syncs", "1")
	_ = flags.Set("concurrent-service-syncs", "1")
	_ = flags.Set("concurrent-rc-syncs", "1")
	_ = flags.Set("concurrent-replicaset-syncs", "1")
	_ = flags.Set("concurrent-namespace-syncs", "1")
	_ = flags.Set("concurrent-serviceaccount-token-syncs", "1")
	_ = flags.Set("terminated-pod-gc-threshold", "0")
	_ = flags.Set("concurrent-gc-syncs", "1")
	_ = flags.Set("large-cluster-size-threshold", "10")
	_ = flags.Set("unhealthy-zone-threshold", "0.7")
	_ = flags.Set("node-monitor-period", "30s")
	_ = flags.Set("node-monitor-grace-period", "60s")
	_ = flags.Set("v", "0")
}
