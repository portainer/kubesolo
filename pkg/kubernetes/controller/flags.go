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

	// Edge-optimised overrides — only applied when not in full mode.
	// When full mode is enabled, upstream Kubernetes defaults are used instead.
	if !s.fullMode {
		// controllers
		_ = flags.Set("controllers", "deployment,replicaset,service,serviceaccount,namespace,attachdetach,endpoint,daemonset,statefulset,root-ca-certificate-publisher-controller,serviceaccount-token-controller,node-ipam-controller,endpointslice-controller,persistentvolume-binder-controller,job-controller,cronjob-controller,garbage-collector-controller,disruption,csrsigning,clusterrole-aggregation")

		_ = flags.Set("profiling", "false")
		_ = flags.Set("terminated-pod-gc-threshold", "20")
		_ = flags.Set("large-cluster-size-threshold", "10")
		_ = flags.Set("unhealthy-zone-threshold", "0.7")

		// sync settings
		_ = flags.Set("concurrent-deployment-syncs", "2")
		_ = flags.Set("concurrent-replicaset-syncs", "2")
		_ = flags.Set("concurrent-job-syncs", "2")
		_ = flags.Set("concurrent-endpoint-syncs", "2")
		_ = flags.Set("concurrent-service-endpoint-syncs", "2")
		_ = flags.Set("concurrent-gc-syncs", "2")
		_ = flags.Set("concurrent-namespace-syncs", "2")
		_ = flags.Set("concurrent-cron-job-syncs", "2")
		_ = flags.Set("concurrent-horizontal-pod-autoscaler-syncs", "2")
		_ = flags.Set("concurrent-rc-syncs", "2")
		_ = flags.Set("concurrent-resource-quota-syncs", "2")
		_ = flags.Set("concurrent-service-syncs", "2")
		_ = flags.Set("concurrent-serviceaccount-token-syncs", "2")
		_ = flags.Set("concurrent-statefulset-syncs", "2")
		_ = flags.Set("concurrent-ttl-after-finished-syncs", "2")
		_ = flags.Set("concurrent-ephemeralvolume-syncs", "2")
		_ = flags.Set("concurrent-validating-admission-policy-status-syncs", "2")
		_ = flags.Set("mirroring-concurrent-service-endpoint-syncs", "2")

		// sync period
		_ = flags.Set("horizontal-pod-autoscaler-sync-period", "60s")
		_ = flags.Set("node-monitor-period", "60s")
		_ = flags.Set("pvclaimbinder-sync-period", "120s")
		_ = flags.Set("resource-quota-sync-period", "15m")
		_ = flags.Set("namespace-sync-period", "15m")
		_ = flags.Set("route-reconciliation-period", "60s")
		_ = flags.Set("attach-detach-reconcile-sync-period", "10m")
		_ = flags.Set("node-monitor-grace-period", "300s")

		// api server interactions
		_ = flags.Set("kube-api-qps", "50")
		_ = flags.Set("kube-api-burst", "100")
	}
}
