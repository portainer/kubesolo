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

func TestRemoveLeafCerts(t *testing.T) {
	t.Run("refuses unsafe paths", func(t *testing.T) {
		for _, p := range []string{"", "/", "."} {
			assert.Errorf(t, removeLeafCerts(p), "removeLeafCerts(%q) must refuse", p)
		}
	})

	t.Run("removes leaf certs but preserves the CA dirs", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "pki")
		for _, sub := range []string{"ca", "request-header", "apiserver", "admin", "kubelet"} {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
		}
		require.NoError(t, removeLeafCerts(dir))

		assert.DirExists(t, filepath.Join(dir, "ca"), "CA dir must be preserved")
		assert.DirExists(t, filepath.Join(dir, "request-header"), "request-header CA dir must be preserved")
		assert.NoDirExists(t, filepath.Join(dir, "apiserver"), "leaf cert dir must be removed")
		assert.NoDirExists(t, filepath.Join(dir, "admin"), "leaf cert dir must be removed")
		assert.NoDirExists(t, filepath.Join(dir, "kubelet"), "leaf cert dir must be removed")
	})
}

func TestNonLoopback(t *testing.T) {
	in := []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("10.0.0.5"),
		net.ParseIP("::1"),
		net.ParseIP("192.168.1.10"),
	}
	got := nonLoopback(in)
	require.Len(t, got, 2)
	assert.Equal(t, "10.0.0.5", got[0].String())
	assert.Equal(t, "192.168.1.10", got[1].String())
}

func TestIPsToStrings(t *testing.T) {
	got := ipsToStrings([]net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("127.0.0.1")})
	assert.Equal(t, []string{"10.0.0.1", "127.0.0.1"}, got)
}

// writeAPIServerCert builds an embedded layout under a temp dir with an
// apiserver.crt generated from the given template, and returns the Embedded.
func writeAPIServerCert(t *testing.T, certPEM []byte) types.Embedded {
	t.Helper()
	pkiDir := filepath.Join(t.TempDir(), "pki")
	apiDir := filepath.Join(pkiDir, "apiserver")
	caDir := filepath.Join(pkiDir, "ca")
	require.NoError(t, os.MkdirAll(apiDir, 0o755))
	// Seed a CA dir so tests can assert it survives invalidation.
	require.NoError(t, os.MkdirAll(caDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(caDir, "ca.crt"), []byte("ca"), 0o644))
	if certPEM != nil {
		require.NoError(t, os.WriteFile(filepath.Join(apiDir, "apiserver.crt"), certPEM, 0o644))
	}
	return types.Embedded{PKIDir: pkiDir, PKIAPIServerDir: apiDir}
}

func selfSignedCert(t *testing.T, notAfter time.Time, ips []net.IP) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "kube-apiserver"},
		NotBefore:    notAfter.Add(-2 * time.Hour),
		NotAfter:     notAfter,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestInvalidateIfIPChanged(t *testing.T) {
	// assertRegenerated asserts the apiserver leaf cert was removed while the CA
	// was preserved, so GenerateAllCertificates re-signs with the existing CA.
	assertRegenerated := func(t *testing.T, e types.Embedded) {
		t.Helper()
		assert.NoFileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"), "stale leaf cert must be removed")
		assert.FileExists(t, filepath.Join(e.PKIDir, "ca", "ca.crt"), "CA must be preserved")
	}

	t.Run("missing cert is a no-op", func(t *testing.T) {
		e := writeAPIServerCert(t, nil)
		require.NoError(t, InvalidateIfIPChanged(e))
		assert.DirExists(t, e.PKIDir, "PKI dir must be left intact when no cert exists yet")
	})

	t.Run("corrupt PEM regenerates leaf certs but keeps the CA", func(t *testing.T) {
		e := writeAPIServerCert(t, []byte("this is not a certificate"))
		require.NoError(t, InvalidateIfIPChanged(e))
		assertRegenerated(t, e)
	})

	t.Run("expired cert regenerates leaf certs but keeps the CA", func(t *testing.T) {
		expired := selfSignedCert(t, time.Now().Add(-1*time.Hour), []net.IP{net.ParseIP("10.0.0.5")})
		e := writeAPIServerCert(t, expired)
		require.NoError(t, InvalidateIfIPChanged(e))
		assertRegenerated(t, e)
	})
}
