package apiserver

import (
	"github.com/portainer/kubesolo/types"
	"github.com/spf13/cobra"
)

func (s *service) configureAPIServerFlags(command *cobra.Command) error {
	flags := command.Flags()

	// networking settings
	_ = flags.Set("insecure-port", "0")
	_ = flags.Set("advertise-address", s.nodeIP)
	_ = flags.Set("service-cluster-ip-range", types.DefaultServiceClusterIPRange)

	// etcd configuration
	_ = flags.Set("etcd-servers", types.DefaultKineEndpoint)

	// security and certificates
	_ = flags.Set("cert-dir", s.pkiAPIServerDir)
	_ = flags.Set("client-ca-file", s.caFile)
	_ = flags.Set("kubelet-client-certificate", s.apiServerCertFile)
	_ = flags.Set("kubelet-client-key", s.apiServerKeyFile)
	_ = flags.Set("service-account-issuer", "kubernetes.default.svc")
	_ = flags.Set("service-account-signing-key-file", s.serviceAccountKeyFile)
	_ = flags.Set("service-account-key-file", s.serviceAccountKeyFile)
	_ = flags.Set("api-audiences", "kubernetes.default.svc")

	// request header authentication (aggregation layer)
	_ = flags.Set("requestheader-client-ca-file", s.requestHeaderCAFile)
	_ = flags.Set("requestheader-allowed-names", "system:auth-proxy")
	_ = flags.Set("requestheader-extra-headers-prefix", "X-Remote-Extra-")
	_ = flags.Set("requestheader-group-headers", "X-Remote-Group")
	_ = flags.Set("requestheader-username-headers", "X-Remote-User")
	_ = flags.Set("proxy-client-cert-file", s.requestHeaderClientCert)
	_ = flags.Set("proxy-client-key-file", s.requestHeaderClientKey)

	// authorization
	_ = flags.Set("allow-privileged", "true")
	_ = flags.Set("authorization-mode", "Node,RBAC")

	// feature gates - disable SizeBasedListCostEstimate to suppress "Error getting keys" messages
	_ = flags.Set("feature-gates", "SizeBasedListCostEstimate=false")

	// Edge-optimised overrides — only applied when not in full mode.
	// When full mode is enabled, upstream Kubernetes defaults are used instead.
	if !s.fullMode {
		// etcd metric collection
		_ = flags.Set("etcd-count-metric-poll-period", "0")
		_ = flags.Set("etcd-db-metric-poll-interval", "0")

		// request throttling and timeouts
		_ = flags.Set("max-requests-inflight", "2000")
		_ = flags.Set("max-mutating-requests-inflight", "1000")
		_ = flags.Set("min-request-timeout", "180")
		_ = flags.Set("request-timeout", "900s")
		_ = flags.Set("kubelet-timeout", "30s")

		// diagnostics
		_ = flags.Set("profiling", "false")

		// admission control
		_ = flags.Set("enable-admission-plugins", "NodeRestriction,ServiceAccount,ValidatingAdmissionWebhook,MutatingAdmissionWebhook,DefaultStorageClass,CertificateApproval,CertificateSigning,CertificateSubjectRestriction,ValidatingAdmissionPolicy,MutatingAdmissionPolicy")
		_ = flags.Set("disable-admission-plugins", "RuntimeClass,PodSecurity,ClusterTrustBundleAttest,DefaultIngressClass,TaintNodesByCondition,DefaultTolerationSeconds,StorageObjectInUseProtection,PersistentVolumeClaimResize,ResourceQuota,LimitRanger,Priority")

		// audit logging
		_ = flags.Set("audit-log-path", "-")
		_ = flags.Set("audit-log-maxage", "0")
		_ = flags.Set("audit-log-maxbackup", "0")
		_ = flags.Set("audit-log-maxsize", "0")
	}

	return nil
}
