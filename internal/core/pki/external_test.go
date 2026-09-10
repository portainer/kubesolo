package pki

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portainer/kubesolo/types"
	"github.com/stretchr/testify/require"
)

// caPair returns a self-signed certificate and its key, PEM encoded.
func caPair(t *testing.T, isCA bool, notAfter time.Time, pkcs8 bool) ([]byte, []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kubernetes-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if pkcs8 {
		der8, err := x509.MarshalPKCS8PrivateKey(key)
		require.NoError(t, err)
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der8}
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(block)
}

// writePair writes the material to a temp dir and returns an Embedded pointing at it.
func writePair(t *testing.T, certPEM, keyPEM []byte) types.Embedded {
	t.Helper()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	require.NoError(t, os.WriteFile(certPath, certPEM, 0600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0600))

	return types.Embedded{
		ExternalCA: true,
		CACerts:    types.CACertificatePaths{Cert: certPath, Key: keyPath},
	}
}

// Both PEM encodings have to be accepted: KubeSolo writes PKCS#1, everything
// else tends to produce PKCS#8.
func TestVerifyExternalCAAccepts(t *testing.T) {
	for name, pkcs8 := range map[string]bool{"pkcs1": false, "pkcs8": true} {
		t.Run(name, func(t *testing.T) {
			certPEM, keyPEM := caPair(t, true, time.Now().Add(24*time.Hour), pkcs8)
			require.NoError(t, VerifyExternalCA(writePair(t, certPEM, keyPEM)))
		})
	}
}

// Each of these would otherwise surface as a TLS failure somewhere else in the
// cluster, with an error naming a leaf certificate rather than the CA.
func TestVerifyExternalCARejects(t *testing.T) {
	t.Run("not a CA", func(t *testing.T) {
		certPEM, keyPEM := caPair(t, false, time.Now().Add(24*time.Hour), false)
		require.ErrorContains(t, VerifyExternalCA(writePair(t, certPEM, keyPEM)), "not a CA certificate")
	})

	t.Run("expired", func(t *testing.T) {
		certPEM, keyPEM := caPair(t, true, time.Now().Add(-time.Hour), false)
		require.ErrorContains(t, VerifyExternalCA(writePair(t, certPEM, keyPEM)), "expired")
	})

	t.Run("key does not match the certificate", func(t *testing.T) {
		certPEM, _ := caPair(t, true, time.Now().Add(24*time.Hour), false)
		_, otherKeyPEM := caPair(t, true, time.Now().Add(24*time.Hour), false)
		require.ErrorContains(t, VerifyExternalCA(writePair(t, certPEM, otherKeyPEM)), "do not match")
	})

	// The case that matters most: a mistyped path must not fall through to
	// generating a fresh CA at that location.
	t.Run("missing file", func(t *testing.T) {
		embedded := types.Embedded{
			ExternalCA: true,
			CACerts:    types.CACertificatePaths{Cert: "/nonexistent/ca.crt", Key: "/nonexistent/ca.key"},
		}
		require.ErrorContains(t, VerifyExternalCA(embedded), "supplied CA")
	})
}

// A supplied CA has to sign the leaf certificates, or the cluster ends up with a
// trust anchor nothing was issued from.
func TestGenerateAllCertificatesSignsWithTheSuppliedCA(t *testing.T) {
	certPEM, keyPEM := caPair(t, true, time.Now().Add(24*time.Hour), false)
	embedded := writePair(t, certPEM, keyPEM)

	base := t.TempDir()
	embedded.NodeName = "talos-cp-1"
	embedded.PKIDir = filepath.Join(base, "pki")
	embedded.PKIAPIServerDir = filepath.Join(base, "pki", "apiserver")
	embedded.PKIKubeletDir = filepath.Join(base, "pki", "kubelet")
	embedded.PKIControllerDir = filepath.Join(base, "pki", "controller-manager")
	embedded.PKIAdminDir = filepath.Join(base, "pki", "admin")
	embedded.PKIWebhookDir = filepath.Join(base, "pki", "webhook")
	embedded.PKIRequestHeaderDir = filepath.Join(base, "pki", "request-header")
	embedded.RequestHeaderCerts = types.RequestHeaderCertificatePaths{
		CACert:     filepath.Join(base, "pki", "request-header", "request-header-ca.crt"),
		CAKey:      filepath.Join(base, "pki", "request-header", "request-header-ca.key"),
		ClientCert: filepath.Join(base, "pki", "request-header", "request-header-client.crt"),
		ClientKey:  filepath.Join(base, "pki", "request-header", "request-header-client.key"),
	}

	require.NoError(t, GenerateAllCertificates(embedded))

	onDisk, err := os.ReadFile(embedded.CACerts.Cert)
	require.NoError(t, err)
	require.Equal(t, certPEM, onDisk, "the supplied CA certificate must not be rewritten")

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(certPEM))

	leafPEM, err := os.ReadFile(filepath.Join(embedded.PKIAPIServerDir, "apiserver.crt"))
	require.NoError(t, err)
	block, _ := pem.Decode(leafPEM)
	require.NotNil(t, block)
	leaf, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	require.NoError(t, err, "the apiserver certificate must chain to the supplied CA")
}
