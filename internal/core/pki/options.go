package pki

import (
	"fmt"
	"net"
	"path/filepath"

	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/internal/system"
	"github.com/portainer/kubesolo/types"
)

// defaultCertOptions returns default options for the specified certificate type
func defaultCertOptions(certType CertificateType, embedded types.Embedded) CertOptions {
	opts := CertOptions{
		Type:         certType,
		NotAfterDays: 365,
		KeySize:      2048,
	}

	switch certType {
	case CACert:
		opts.CommonName = "kubernetes-ca"
		opts.Organization = []string{"Kubernetes"}
		opts.NotAfterDays = 3650
		opts.CertDir = embedded.CACerts.Cert
		opts.KeyDir = embedded.CACerts.Key

	case KubeletCert:
		hostname := system.GetHostname()

		opts.CommonName = fmt.Sprintf("system:node:%s", hostname)
		opts.Organization = []string{"system:nodes"}
		opts.DNSNames = []string{hostname, "localhost"}
		opts.SignerCertDir = embedded.CACerts.Cert
		opts.SignerKeyDir = embedded.CACerts.Key
		opts.CertDir = filepath.Join(embedded.PKIKubeletDir, "kubelet.crt")
		opts.KeyDir = filepath.Join(embedded.PKIKubeletDir, "kubelet.key")

		ips, err := network.GetLocalIPs()
		if err == nil {
			opts.IPAddresses = ips
		} else {
			opts.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}

	case APIServerCert:
		opts.CommonName = "kube-apiserver"
		opts.Organization = []string{"Kubernetes"}
		opts.DNSNames = []string{
			"kubernetes",
			"kubernetes.default",
			"kubernetes.default.svc",
			"kubernetes.default.svc.cluster",
			"kubernetes.default.svc.cluster.local",
			"localhost",
		}
		opts.IPAddresses = []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP(types.DefaultKubernetesServiceIP),
		}
		opts.SignerCertDir = embedded.CACerts.Cert
		opts.SignerKeyDir = embedded.CACerts.Key
		opts.CertDir = filepath.Join(embedded.PKIAPIServerDir, "apiserver.crt")
		opts.KeyDir = filepath.Join(embedded.PKIAPIServerDir, "apiserver.key")

		localIPs, err := network.GetLocalIPs()
		if err == nil {
			opts.IPAddresses = append(opts.IPAddresses, localIPs...)
		}

	case ControllerManagerCert:
		opts.CommonName = "system:kube-controller-manager"
		opts.Organization = []string{"system:kube-controller-manager"}
		opts.SignerCertDir = embedded.CACerts.Cert
		opts.SignerKeyDir = embedded.CACerts.Key
		opts.CertDir = filepath.Join(embedded.PKIControllerDir, "controller-manager.crt")
		opts.KeyDir = filepath.Join(embedded.PKIControllerDir, "controller-manager.key")

	case AdminCert:
		opts.CommonName = "kubesolo-admin"
		opts.Organization = []string{"system:masters"}
		opts.DNSNames = []string{"localhost"}
		localIPs, err := network.GetLocalIPs()
		if err == nil {
			opts.IPAddresses = localIPs
		} else {
			opts.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}
		opts.SignerCertDir = embedded.CACerts.Cert
		opts.SignerKeyDir = embedded.CACerts.Key
		opts.CertDir = filepath.Join(embedded.PKIAdminDir, "admin.crt")
		opts.KeyDir = filepath.Join(embedded.PKIAdminDir, "admin.key")

	case WebhookCert:
		opts.CommonName = "kubesolo-webhook"
		opts.Organization = []string{"system:masters"}
		opts.DNSNames = []string{"localhost", "kubesolo-webhook", "kubesolo-webhook.default", "kubesolo-webhook.default.svc"}
		opts.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		opts.SignerCertDir = embedded.CACerts.Cert
		opts.SignerKeyDir = embedded.CACerts.Key
		opts.CertDir = filepath.Join(embedded.PKIWebhookDir, "webhook.crt")
		opts.KeyDir = filepath.Join(embedded.PKIWebhookDir, "webhook.key")
	}

	return opts
}
