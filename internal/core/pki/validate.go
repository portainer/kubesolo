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

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// InvalidateIfIPChanged checks whether the existing apiserver certificate covers the
// advertised node IP. If not, it removes the leaf certificates so that
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

	// Verify the cert covers the node IP we are going to advertise. The apiserver
	// cert SANs are scoped to this IP (plus the service IP and localhost), so
	// comparing against every local IP would wrongly fire on unrelated
	// interfaces (public NIC, cni0) and regenerate on every boot.
	nodeIP := net.ParseIP(embedded.NodeIP)
	if nodeIP == nil || nodeIP.IsLoopback() {
		return nil
	}

	for _, san := range cert.IPAddresses {
		if nodeIP.Equal(san) {
			return nil
		}
	}

	log.Warn().
		Str("component", "pki").
		Str("missing_ip", nodeIP.String()).
		Strs("cert_ips", ipsToStrings(cert.IPAddresses)).
		Msg("node IP not found in existing certificate SANs — regenerating leaf certificates")
	return removeLeafCerts(embedded.PKIDir)
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
	// Reject a symlinked PKI directory: os.ReadDir follows it, so a symlink to an
	// unexpected location (e.g. /etc) would redirect the per-entry deletions
	// there. os.RemoveAll on the directory itself would have removed the symlink
	// instead, so this path is only reachable with the new per-entry approach.
	info, err := os.Lstat(pkiDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to modify PKI directory: %q is a symlink", pkiDir)
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

func ipsToStrings(ips []net.IP) []string {
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out
}
