package pki

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/internal/system"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// defaultCertOptions returns default options for the specified certificate type
// it sets the relevant fields for the certificate type, including the local IPv4 addresses
// the supported certificate types are CACert, KubeletCert, APIServerCert, ControllerManagerCert, AdminCert, and WebhookCert
func defaultCertOptions(certType CertificateType, embedded types.Embedded) CertOptions {
	opts := CertOptions{
		Type:         certType,
		NotAfterDays: 365,
		KeySize:      2048,
	}

	ipAddresses := []net.IP{}
	ips, err := network.GetLocalIPs()
	if err == nil {
		ipAddresses = ips
	} else {
		ipAddresses = []net.IP{net.ParseIP("127.0.0.1")}
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
		opts.IPAddresses = ipAddresses

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
		opts.IPAddresses = []net.IP{net.ParseIP(types.DefaultKubernetesServiceIP)}
		opts.IPAddresses = append(opts.IPAddresses, ipAddresses...)
		if len(embedded.APIServerExtraSANs) > 0 {
			addExtraSANs(&opts, embedded.APIServerExtraSANs)
		}
		opts.SignerCertDir = embedded.CACerts.Cert
		opts.SignerKeyDir = embedded.CACerts.Key
		opts.CertDir = filepath.Join(embedded.PKIAPIServerDir, "apiserver.crt")
		opts.KeyDir = filepath.Join(embedded.PKIAPIServerDir, "apiserver.key")

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
		opts.IPAddresses = ipAddresses
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

// addExtraSANs adds extra SANs to the certificate options
// it validates the SANs and adds them to the certificate options
// if the SAN is a valid IPv4 address, it adds it to the IPAddresses field
// if the SAN is a valid DNS name, it adds it to the DNSNames field
// if the SAN is invalid, it logs a warning and skips it
func addExtraSANs(opts *CertOptions, extraSANs []string) {
	for _, san := range extraSANs {
		san = strings.TrimSpace(san)
		if san == "" {
			continue
		}

		if network.IsIPv4Address(san) {
			opts.IPAddresses = append(opts.IPAddresses, net.ParseIP(san))
		} else if network.IsDNSName(san) {
			opts.DNSNames = append(opts.DNSNames, san)
		} else {
			log.Warn().Str("component", "pki").Msgf("invalid SAN: %s", san)
			continue
		}
	}
}
