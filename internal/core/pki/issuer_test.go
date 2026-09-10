package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portainer/kubesolo/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signedLeaf returns a CA certificate and a leaf signed by it, PEM encoded.
// Both carry the same subject that KubeSolo and Talos use, so a test that
// passes cannot be doing so by comparing issuer names.
func signedLeaf(t *testing.T, ip string) (caPEM, leafPEM []byte) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"kubernetes"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	require.NoError(t, err)

	ca, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "kube-apiserver"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP(ip)},
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, leafKey.Public(), caKey)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
}

// layout writes a PKI directory holding the leaf, with CACerts pointing at
// whichever CA the caller wants KubeSolo to be configured with.
func layout(t *testing.T, ip string, leafPEM, configuredCAPEM []byte) types.Embedded {
	t.Helper()

	base := t.TempDir()
	pkiDir := filepath.Join(base, "pki")
	apiDir := filepath.Join(pkiDir, "apiserver")
	caDir := filepath.Join(pkiDir, "ca")
	require.NoError(t, os.MkdirAll(apiDir, 0o755))
	require.NoError(t, os.MkdirAll(caDir, 0o755))

	caPath := filepath.Join(caDir, "ca.crt")
	require.NoError(t, os.WriteFile(caPath, configuredCAPEM, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(apiDir, "apiserver.crt"), leafPEM, 0o644))

	return types.Embedded{
		PKIDir:          pkiDir,
		PKIAPIServerDir: apiDir,
		NodeIP:          ip,
		CACerts:         types.CACertificatePaths{Cert: caPath},
	}
}

// Pointing an existing install at a different CA leaves every leaf signed by
// the old one. Nothing else in InvalidateIfStale notices: the certificates have
// not expired and their SANs are unchanged.
func TestInvalidateIfStaleDetectsAChangedCA(t *testing.T) {
	t.Run("leaf signed by the configured CA is kept", func(t *testing.T) {
		caPEM, leafPEM := signedLeaf(t, "10.0.0.5")
		e := layout(t, "10.0.0.5", leafPEM, caPEM)

		require.NoError(t, InvalidateIfStale(e))
		assert.FileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"))
	})

	t.Run("leaf signed by a different CA is regenerated", func(t *testing.T) {
		_, leafPEM := signedLeaf(t, "10.0.0.5")
		otherCAPEM, _ := signedLeaf(t, "10.0.0.5")
		e := layout(t, "10.0.0.5", leafPEM, otherCAPEM)

		require.NoError(t, InvalidateIfStale(e))
		assert.NoFileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"), "leaf signed by the old CA must be removed")
		assert.FileExists(t, filepath.Join(e.PKIDir, "ca", "ca.crt"), "the configured CA must be preserved")
	})
}
