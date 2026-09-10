package pki

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// InvalidateIfStale checks whether the existing apiserver certificate still
// matches the configuration. If it does not, it removes the leaf certificates so
// that GenerateAllCertificates will re-sign fresh ones on the next call.
//
// Two things can make it stale. The advertised node IP may have moved, which
// handles DHCP address changes between restarts: the old certs embed the
// previous IP in their SANs, causing TLS failures until the PKI is regenerated.
// The configured extra SANs may also have changed — adding one to the
// configuration has no other trigger to re-sign, so without this the new name is
// silently rejected by TLS until the certificate expires or the node IP moves.
//
// The CA is deliberately preserved (see removeLeafCerts): regenerating it on
// every IP change would rotate the cluster's trust anchor and force every
// previously distributed kubeconfig to be updated. Keeping the CA stable lets
// re-signed leaf certs (and existing client certs) keep validating.
func InvalidateIfStale(embedded types.Embedded) error {
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
	//
	// A loopback or unparseable node IP is not checked: it is not embedded as a
	// distinguishing SAN, so there is nothing to compare against.
	if nodeIP := net.ParseIP(embedded.NodeIP); nodeIP != nil && !nodeIP.IsLoopback() && !containsIP(cert.IPAddresses, nodeIP) {
		log.Warn().
			Str("component", "pki").
			Str("missing_ip", nodeIP.String()).
			Strs("cert_ips", ipsToStrings(cert.IPAddresses)).
			Msg("node IP not found in existing certificate SANs — regenerating leaf certificates")
		return removeLeafCerts(embedded.PKIDir)
	}

	if missing := missingExtraSANs(cert, embedded.APIServerExtraSANs); len(missing) > 0 {
		log.Warn().
			Str("component", "pki").
			Strs("missing_sans", missing).
			Strs("cert_ips", ipsToStrings(cert.IPAddresses)).
			Strs("cert_dns_names", cert.DNSNames).
			Msg("configured extra SANs not found in existing certificate — regenerating leaf certificates")
		return removeLeafCerts(embedded.PKIDir)
	}

	return nil
}

// missingExtraSANs reports which configured extra SANs the certificate does not
// carry.
//
// Classification mirrors addExtraSANs exactly, skipping values that are neither
// an IPv4 address nor a DNS name. That is load-bearing rather than tidiness: a
// SAN generation discards would otherwise be reported missing on every boot, and
// the leaf certificates would be destroyed and re-signed each time.
func missingExtraSANs(cert *x509.Certificate, extraSANs []string) []string {
	var missing []string

	for _, san := range extraSANs {
		san = strings.TrimSpace(san)
		if san == "" {
			continue
		}

		switch {
		case network.IsIPv4Address(san):
			if !containsIP(cert.IPAddresses, net.ParseIP(san)) {
				missing = append(missing, san)
			}
		case network.IsDNSName(san):
			if !containsDNSName(cert.DNSNames, san) {
				missing = append(missing, san)
			}
		}
	}

	return missing
}

func containsIP(haystack []net.IP, needle net.IP) bool {
	return slices.ContainsFunc(haystack, needle.Equal)
}

// containsDNSName compares without regard to case, as DNS itself and Go's own
// certificate verification both do. A configuration that only differs in case
// still matches the certificate, so re-signing would achieve nothing.
func containsDNSName(haystack []string, needle string) bool {
	return slices.ContainsFunc(haystack, func(name string) bool {
		return strings.EqualFold(name, needle)
	})
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

// VerifyExternalCA checks CA material supplied via pki.caCert and pki.caKey
// before anything is signed with it.
//
// This has to fail loudly rather than fall through. generateCertificate skips a
// certificate that already exists and generates one that does not, so an
// unreadable or mistyped path would not be an error at all: KubeSolo would write
// a brand new self-signed CA at the operator's path and carry on, having quietly
// replaced the cluster's trust anchor with one nothing else trusts. The symptom
// would be every other component failing TLS for reasons that never name the CA.
func VerifyExternalCA(embedded types.Embedded) error {
	cert, key, err := loadCertificateAndKey(embedded.CACerts.Cert, embedded.CACerts.Key)
	if err != nil {
		return fmt.Errorf("supplied CA (pki.caCert %s, pki.caKey %s): %v", embedded.CACerts.Cert, embedded.CACerts.Key, err)
	}

	if !cert.IsCA {
		return fmt.Errorf("supplied CA %s is not a CA certificate (its basic constraints do not set CA:TRUE), so it cannot sign the control plane's certificates", embedded.CACerts.Cert)
	}

	// A cert and key that do not belong together produce certificates that fail
	// verification everywhere, with errors that point at the leaf rather than here.
	public, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("supplied CA %s carries a %T public key, but KubeSolo signs with RSA keys only", embedded.CACerts.Cert, cert.PublicKey)
	}
	if public.N.Cmp(key.N) != 0 {
		return fmt.Errorf("supplied CA %s and key %s do not match: the key does not belong to that certificate", embedded.CACerts.Cert, embedded.CACerts.Key)
	}

	if time.Now().After(cert.NotAfter) {
		return fmt.Errorf("supplied CA %s expired on %s; KubeSolo will not sign with an expired CA", embedded.CACerts.Cert, cert.NotAfter.Format(time.RFC3339))
	}

	log.Info().Str("component", "pki").
		Str("ca", embedded.CACerts.Cert).
		Str("subject", cert.Subject.String()).
		Time("expires", cert.NotAfter).
		Msg("signing with the supplied CA; KubeSolo will not generate, rotate or remove it")

	return nil
}
