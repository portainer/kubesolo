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
// current node IPs. If not, it removes the entire PKI directory so that
// GenerateAllCertificates will produce fresh certificates on the next call.
//
// This handles DHCP address changes between restarts: the old certs embed the
// previous IP in their SANs, causing TLS failures until the PKI is regenerated.
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
			Msg("existing certificate is corrupt (PEM decode failed) — removing PKI directory for regeneration")
		return removePKIDir(embedded.PKIDir)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Warn().Str("component", "pki").Str("cert", certPath).
			Msg("existing certificate is corrupt (parse failed) — removing PKI directory for regeneration")
		return removePKIDir(embedded.PKIDir)
	}

	if time.Now().After(cert.NotAfter) {
		log.Warn().Str("component", "pki").Time("expired_at", cert.NotAfter).
			Msg("existing certificate has expired — removing PKI directory for regeneration")
		return removePKIDir(embedded.PKIDir)
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
				Msg("node IP not found in existing certificate SANs — removing PKI directory for regeneration")
			return removePKIDir(embedded.PKIDir)
		}
	}

	return nil
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
