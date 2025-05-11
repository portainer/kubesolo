package apiserver

import (
	"fmt"

	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/types"
	"github.com/spf13/cobra"
)

func (s *service) configureAPIServerFlags(command *cobra.Command) error {
	nodeIP, err := network.GetNodeIP()
	if err != nil {
		return fmt.Errorf("failed to get node IP address: %v", err)
	}

	flags := command.Flags()
	_ = flags.Set("etcd-servers", types.DefaultKineEndpoint)
	_ = flags.Set("insecure-port", "0")
	_ = flags.Set("secure-port", "6443")
	_ = flags.Set("bind-address", "0.0.0.0")
	_ = flags.Set("advertise-address", nodeIP)
	_ = flags.Set("cert-dir", s.pkiAPIServerDir)
	_ = flags.Set("service-account-issuer", "kubernetes.default.svc")
	_ = flags.Set("service-account-signing-key-file", s.serviceAccountKeyFile)
	_ = flags.Set("service-account-key-file", s.serviceAccountKeyFile)
	_ = flags.Set("api-audiences", "kubernetes.default.svc")
	_ = flags.Set("service-cluster-ip-range", types.DefaultServiceClusterIPRange)
	_ = flags.Set("allow-privileged", "true")
	_ = flags.Set("authorization-mode", "Node,RBAC")
	_ = flags.Set("client-ca-file", s.caFile)
	_ = flags.Set("kubelet-client-certificate", s.apiServerCertFile)
	_ = flags.Set("kubelet-client-key", s.apiServerKeyFile)
	_ = flags.Set("enable-admission-plugins", "NodeRestriction,ServiceAccount,MutatingAdmissionWebhook")
	_ = flags.Set("disable-admission-plugins", "ValidatingAdmissionWebhook,RuntimeClass,PodSecurity,CertificateApproval,CertificateSigning,ClusterTrustBundleAttest,CertificateSubjectRestriction,MutatingAdmissionPolicy,ValidatingAdmissionPolicy,DefaultIngressClass,TaintNodesByCondition,Priority,DefaultTolerationSeconds,DefaultStorageClass,StorageObjectInUseProtection,PersistentVolumeClaimResize,ResourceQuota,LimitRanger")
	// _ = flags.Set("feature-gates", "AllAlpha=false,AllBeta=false")
	_ = flags.Set("max-requests-inflight", "50")
	_ = flags.Set("max-mutating-requests-inflight", "25")
	_ = flags.Set("etcd-compaction-interval", "30m")
	_ = flags.Set("etcd-count-metric-poll-period", "1m")
	_ = flags.Set("min-request-timeout", "60")
	_ = flags.Set("watch-cache", "false")
	_ = flags.Set("event-ttl", "1h")
	_ = flags.Set("enable-bootstrap-token-auth", "false")
	_ = flags.Set("enable-garbage-collector", "false")
	_ = flags.Set("profiling", "false")
	_ = flags.Set("audit-log-path", "-")
	_ = flags.Set("audit-log-maxage", "0")
	_ = flags.Set("audit-log-maxbackup", "0")
	_ = flags.Set("audit-log-maxsize", "0")
	_ = flags.Set("min-request-timeout", "30")
	_ = flags.Set("request-timeout", "300s")

	return nil
}
