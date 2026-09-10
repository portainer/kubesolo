package apiserver

import (
	"strings"

	"github.com/portainer/kubesolo/types"
	"github.com/spf13/cobra"
)

func (s *service) configureAPIServerFlags(command *cobra.Command) error {
	flags := command.Flags()

	// networking settings
	_ = flags.Set("insecure-port", "0")
	_ = flags.Set("advertise-address", s.nodeIP)
	_ = flags.Set("service-cluster-ip-range", types.DefaultServiceClusterIPRange)

	// etcd configuration. s.etcdEndpoints is the kine loopback address unless the
	// host runs its own etcd, so there is one code path either way. A host-managed
	// etcd almost always wants client certificates; kine wants none.
	_ = flags.Set("etcd-servers", strings.Join(s.etcdEndpoints, ","))
	if s.etcdCAFile != "" {
		_ = flags.Set("etcd-cafile", s.etcdCAFile)
	}
	if s.etcdCertFile != "" && s.etcdKeyFile != "" {
		_ = flags.Set("etcd-certfile", s.etcdCertFile)
		_ = flags.Set("etcd-keyfile", s.etcdKeyFile)
	}

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

	// TLS bootstrapping. Off unless a token is configured: with it on, anything
	// that can present a valid bootstrap token can obtain a node certificate, so
	// it is not something to enable for a cluster that has no use for it.
	if s.bootstrapToken != "" {
		_ = flags.Set("enable-bootstrap-token-auth", "true")
	}

	// authorization
	_ = flags.Set("allow-privileged", "true")
	_ = flags.Set("authorization-mode", "Node,RBAC")

	// feature gates - disable SizeBasedListCostEstimate to suppress "Error getting keys" messages
	_ = flags.Set("feature-gates", "SizeBasedListCostEstimate=false")

	return nil
}
