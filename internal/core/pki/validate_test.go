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

func TestRemovePKIDir(t *testing.T) {
	t.Run("refuses unsafe paths", func(t *testing.T) {
		for _, p := range []string{"", "/", "."} {
			assert.Errorf(t, removePKIDir(p), "removePKIDir(%q) must refuse", p)
		}
	})

	t.Run("removes a real directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "pki")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, removePKIDir(dir))
		assert.NoDirExists(t, dir)
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
	require.NoError(t, os.MkdirAll(apiDir, 0o755))
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
	t.Run("missing cert is a no-op", func(t *testing.T) {
		e := writeAPIServerCert(t, nil)
		require.NoError(t, InvalidateIfIPChanged(e))
		assert.DirExists(t, e.PKIDir, "PKI dir must be left intact when no cert exists yet")
	})

	t.Run("corrupt PEM removes the PKI dir", func(t *testing.T) {
		e := writeAPIServerCert(t, []byte("this is not a certificate"))
		require.NoError(t, InvalidateIfIPChanged(e))
		assert.NoDirExists(t, e.PKIDir, "corrupt cert must trigger PKI regeneration")
	})

	t.Run("expired cert removes the PKI dir", func(t *testing.T) {
		expired := selfSignedCert(t, time.Now().Add(-1*time.Hour), []net.IP{net.ParseIP("10.0.0.5")})
		e := writeAPIServerCert(t, expired)
		require.NoError(t, InvalidateIfIPChanged(e))
		assert.NoDirExists(t, e.PKIDir, "expired cert must trigger PKI regeneration")
	})
}
