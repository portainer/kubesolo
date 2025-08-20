package apiserver

import (
	"fmt"

	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/spf13/cobra"
)

func (s *service) configureAPIServerFlags(command *cobra.Command) error {
	nodeIP, err := network.GetNodeIP()
	if err != nil {
		return fmt.Errorf("failed to get node IP address: %v", err)
	}

	flags := command.Flags()

	// networking settings
	_ = flags.Set("insecure-port", "0")
	_ = flags.Set("secure-port", "6443")
	_ = flags.Set("bind-address", "0.0.0.0")
	_ = flags.Set("advertise-address", nodeIP)
	_ = flags.Set("service-cluster-ip-range", types.DefaultServiceClusterIPRange)

	// etcd configuration
	_ = flags.Set("etcd-servers", types.DefaultKineEndpoint)
	_ = flags.Set("etcd-compaction-interval", "0")
	_ = flags.Set("etcd-count-metric-poll-period", "0")
	_ = flags.Set("etcd-db-metric-poll-interval", "0")

	// security and certificates
	_ = flags.Set("cert-dir", s.pkiAPIServerDir)
	_ = flags.Set("client-ca-file", s.caFile)
	_ = flags.Set("kubelet-client-certificate", s.apiServerCertFile)
	_ = flags.Set("kubelet-client-key", s.apiServerKeyFile)
	_ = flags.Set("service-account-issuer", "kubernetes.default.svc")
	_ = flags.Set("service-account-signing-key-file", s.serviceAccountKeyFile)
	_ = flags.Set("service-account-key-file", s.serviceAccountKeyFile)
	_ = flags.Set("api-audiences", "kubernetes.default.svc")

	// authorization and admission
	_ = flags.Set("allow-privileged", "true")
	_ = flags.Set("authorization-mode", "Node,RBAC")
	_ = flags.Set("enable-admission-plugins", "NodeRestriction,ServiceAccount,ValidatingAdmissionWebhook,MutatingAdmissionWebhook,DefaultStorageClass")
	_ = flags.Set("disable-admission-plugins", "RuntimeClass,PodSecurity,CertificateApproval,CertificateSigning,ClusterTrustBundleAttest,CertificateSubjectRestriction,MutatingAdmissionPolicy,ValidatingAdmissionPolicy,DefaultIngressClass,TaintNodesByCondition,Priority,DefaultTolerationSeconds,StorageObjectInUseProtection,PersistentVolumeClaimResize,ResourceQuota,LimitRanger")
	_ = flags.Set("enable-bootstrap-token-auth", "false")

	// performance and resource limits
	_ = flags.Set("max-requests-inflight", "2000")
	_ = flags.Set("max-mutating-requests-inflight", "1000")
	_ = flags.Set("min-request-timeout", "180")
	_ = flags.Set("request-timeout", "900s")
	_ = flags.Set("kubelet-timeout", "30s")

	// caching and storage
	v, err := mem.VirtualMemory()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get host memory")
	}

	watchCache := "true"
	if v.Total < types.DefaultOSMemoryLimit {
		watchCache = "false"
	}
	_ = flags.Set("watch-cache", watchCache)
	_ = flags.Set("event-ttl", "1h")

	// features and garbage collection
	_ = flags.Set("enable-garbage-collector", "true")
	_ = flags.Set("profiling", "false")

	// audit logging
	_ = flags.Set("audit-log-path", "-")
	_ = flags.Set("audit-log-maxage", "0")
	_ = flags.Set("audit-log-maxbackup", "0")
	_ = flags.Set("audit-log-maxsize", "0")
	return nil
}
