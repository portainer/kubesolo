package pki

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
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

	// Cert file exists — any failure to read or parse it means the PKI is
	// unusable. Fall through to removal rather than leaving a broken state.
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		log.Warn().Str("component", "pki").Str("cert", certPath).
			Msg("existing certificate is unreadable — removing PKI directory for regeneration")
		return removePKIDir(embedded.PKIDir)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		log.Warn().Str("component", "pki").Str("cert", certPath).
			Msg("existing certificate is corrupt (PEM decode failed) — removing PKI directory for regeneration")
		return removePKIDir(embedded.PKIDir)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Warn().Str("component", "pki").Str("cert", certPath).
			Msg("existing certificate is corrupt (parse failed) — removing PKI directory for regeneration")
		return removePKIDir(embedded.PKIDir)
	}

	currentIPs, err := network.GetLocalIPs()
	if err != nil || len(currentIPs) == 0 {
		return nil
	}

	// Loopback (127.0.0.1) is always present in both the current IP list and
	// the cert SANs, so comparing it would always produce a match even when
	// the real node IP has changed. Only compare non-loopback IPs.
	nodeIPs := nonLoopback(currentIPs)
	if len(nodeIPs) == 0 {
		return nil
	}

	for _, current := range nodeIPs {
		for _, san := range cert.IPAddresses {
			if current.Equal(san) {
				return nil
			}
		}
	}

	log.Warn().
		Str("component", "pki").
		Strs("cert_ips", ipsToStrings(cert.IPAddresses)).
		Strs("node_ips", ipsToStrings(nodeIPs)).
		Msg("node IP not found in existing certificate SANs — removing PKI directory for regeneration")

	return removePKIDir(embedded.PKIDir)
}

func removePKIDir(pkiDir string) error {
	if pkiDir == "" || pkiDir == "/" || pkiDir == "." {
		return fmt.Errorf("refusing to remove PKI directory: unsafe path %q", pkiDir)
	}
	return os.RemoveAll(pkiDir)
}

func nonLoopback(ips []net.IP) []net.IP {
	var out []net.IP
	for _, ip := range ips {
		if !ip.IsLoopback() {
			out = append(out, ip)
		}
	}
	return out
}

func ipsToStrings(ips []net.IP) []string {
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out
}
