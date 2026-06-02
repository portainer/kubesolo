package pki

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"

	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// InvalidateIfIPChanged checks whether the existing apiserver certificate covers the
// current node IPs. If not, it removes the entire PKI directory so that
// GenerateAllCertificates will produce fresh certificates on the next call.
//
// This handles DHCP address changes between restarts: the old certs embed the
// previous IP in their SANs, causing TLS failures until the PKI is regenerated.
func InvalidateIfIPChanged(embedded types.Embedded) error {
	certPath := filepath.Join(embedded.PKIAPIServerDir, "apiserver.crt")

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		return nil
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}

	currentIPs, err := network.GetLocalIPs()
	if err != nil || len(currentIPs) == 0 {
		return nil
	}

	for _, current := range currentIPs {
		for _, san := range cert.IPAddresses {
			if current.Equal(san) {
				return nil
			}
		}
	}

	log.Warn().
		Str("component", "pki").
		Strs("cert_ips", ipsToStrings(cert.IPAddresses)).
		Strs("node_ips", ipsToStrings(currentIPs)).
		Msg("node IP not found in existing certificate SANs — removing PKI directory for regeneration")

	return os.RemoveAll(embedded.PKIDir)
}

func ipsToStrings(ips []net.IP) []string {
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out
}
