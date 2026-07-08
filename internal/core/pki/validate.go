package pki

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// InvalidateIfIPChanged checks whether the existing apiserver certificate covers the
// current node IPs. If not, it removes the leaf certificates so that
// GenerateAllCertificates will re-sign fresh certificates on the next call.
//
// This handles DHCP address changes between restarts: the old certs embed the
// previous IP in their SANs, causing TLS failures until the PKI is regenerated.
//
// The CA is deliberately preserved (see removeLeafCerts): regenerating it on
// every IP change would rotate the cluster's trust anchor and force every
// previously distributed kubeconfig to be updated. Keeping the CA stable lets
// re-signed leaf certs (and existing client certs) keep validating.
func InvalidateIfIPChanged(embedded types.Embedded) error {
	certPath := filepath.Join(embedded.PKIAPIServerDir, "apiserver.crt")

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		log.Warn().Str("component", "pki").Str("cert", certPath).
			Msg("existing certificate is corrupt (PEM decode failed) — regenerating leaf certificates")
		return removeLeafCerts(embedded.PKIDir)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Warn().Str("component", "pki").Str("cert", certPath).
			Msg("existing certificate is corrupt (parse failed) — regenerating leaf certificates")
		return removeLeafCerts(embedded.PKIDir)
	}

	if time.Now().After(cert.NotAfter) {
		log.Warn().Str("component", "pki").Time("expired_at", cert.NotAfter).
			Msg("existing certificate has expired — regenerating leaf certificates")
		return removeLeafCerts(embedded.PKIDir)
	}

	currentIPs, err := network.GetLocalIPs()
	if err != nil {
		log.Warn().Str("component", "pki").Err(err).
			Msg("could not enumerate local IPs — skipping PKI invalidation check")
		return nil
	}
	if len(currentIPs) == 0 {
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
		covered := false
		for _, san := range cert.IPAddresses {
			if current.Equal(san) {
				covered = true
				break
			}
		}
		if !covered {
			log.Warn().
				Str("component", "pki").
				Str("missing_ip", current.String()).
				Strs("cert_ips", ipsToStrings(cert.IPAddresses)).
				Msg("node IP not found in existing certificate SANs — regenerating leaf certificates")
			return removeLeafCerts(embedded.PKIDir)
		}
	}

	return nil
}

// caDirNames are the PKI subdirectories preserved across regeneration. Keeping
// the CA and request-header CA stable avoids rotating the cluster trust anchor
// (which would invalidate every distributed kubeconfig).
var caDirNames = map[string]bool{"ca": true, "request-header": true}

// removeLeafCerts removes every entry under pkiDir except the CA directories, so
// GenerateAllCertificates re-signs fresh leaf certificates with the existing CA.
func removeLeafCerts(pkiDir string) error {
	if pkiDir == "" || pkiDir == "/" || pkiDir == "." {
		return fmt.Errorf("refusing to modify PKI directory: unsafe path %q", pkiDir)
	}
	entries, err := os.ReadDir(pkiDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if caDirNames[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(pkiDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
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
