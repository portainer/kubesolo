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

	t.Run("refuses a symlinked PKI directory", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "real")
		require.NoError(t, os.MkdirAll(target, 0o755))
		link := filepath.Join(t.TempDir(), "pki-link")
		require.NoError(t, os.Symlink(target, link))
		assert.Error(t, removeLeafCerts(link), "a symlinked PKI dir must be refused")
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

func TestIPsToStrings(t *testing.T) {
	got := ipsToStrings([]net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("127.0.0.1")})
	assert.Equal(t, []string{"10.0.0.1", "127.0.0.1"}, got)
}

// writeAPIServerCert builds an embedded layout under a temp dir with an
// apiserver.crt generated from the given template, and returns the Embedded
// with NodeIP set (the IP the invalidation check compares against).
func writeAPIServerCert(t *testing.T, nodeIP string, certPEM []byte) types.Embedded {
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
	return types.Embedded{PKIDir: pkiDir, PKIAPIServerDir: apiDir, NodeIP: nodeIP}
}

func selfSignedCert(t *testing.T, notAfter time.Time, ips []net.IP) []byte {
	t.Helper()
	return selfSignedCertWithNames(t, notAfter, ips, nil)
}

func selfSignedCertWithNames(t *testing.T, notAfter time.Time, ips []net.IP, dnsNames []string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "kube-apiserver"},
		NotBefore:    notAfter.Add(-2 * time.Hour),
		NotAfter:     notAfter,
		IPAddresses:  ips,
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestInvalidateIfStale(t *testing.T) {
	// assertRegenerated asserts the apiserver leaf cert was removed while the CA
	// was preserved, so GenerateAllCertificates re-signs with the existing CA.
	assertRegenerated := func(t *testing.T, e types.Embedded) {
		t.Helper()
		assert.NoFileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"), "stale leaf cert must be removed")
		assert.FileExists(t, filepath.Join(e.PKIDir, "ca", "ca.crt"), "CA must be preserved")
	}

	t.Run("missing cert is a no-op", func(t *testing.T) {
		e := writeAPIServerCert(t, "10.0.0.5", nil)
		require.NoError(t, InvalidateIfStale(e))
		assert.DirExists(t, e.PKIDir, "PKI dir must be left intact when no cert exists yet")
	})

	t.Run("corrupt PEM regenerates leaf certs but keeps the CA", func(t *testing.T) {
		e := writeAPIServerCert(t, "10.0.0.5", []byte("this is not a certificate"))
		require.NoError(t, InvalidateIfStale(e))
		assertRegenerated(t, e)
	})

	t.Run("expired cert regenerates leaf certs but keeps the CA", func(t *testing.T) {
		expired := selfSignedCert(t, time.Now().Add(-1*time.Hour), []net.IP{net.ParseIP("10.0.0.5")})
		e := writeAPIServerCert(t, "10.0.0.5", expired)
		require.NoError(t, InvalidateIfStale(e))
		assertRegenerated(t, e)
	})

	t.Run("node IP covered by cert is a no-op", func(t *testing.T) {
		valid := selfSignedCert(t, time.Now().Add(24*time.Hour), []net.IP{net.ParseIP("10.0.0.5")})
		e := writeAPIServerCert(t, "10.0.0.5", valid)
		require.NoError(t, InvalidateIfStale(e))
		assert.FileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"), "cert covering the node IP must be kept")
	})

	t.Run("node IP not covered regenerates leaf certs but keeps the CA", func(t *testing.T) {
		// Cert only covers a stale IP; the current node IP is different.
		valid := selfSignedCert(t, time.Now().Add(24*time.Hour), []net.IP{net.ParseIP("10.0.0.5")})
		e := writeAPIServerCert(t, "192.168.1.10", valid)
		require.NoError(t, InvalidateIfStale(e))
		assertRegenerated(t, e)
	})

	t.Run("only other local IPs change is a no-op", func(t *testing.T) {
		// The node IP is covered even though the cert does not list unrelated
		// interfaces (public NIC, cni0) — those must not trigger regeneration.
		valid := selfSignedCert(t, time.Now().Add(24*time.Hour), []net.IP{net.ParseIP("10.0.0.5")})
		e := writeAPIServerCert(t, "10.0.0.5", valid)
		require.NoError(t, InvalidateIfStale(e))
		assert.FileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"), "unrelated local IPs must not trigger regeneration")
	})
}

// TestInvalidateIfStaleExtraSANs covers the case that previously went unnoticed:
// adding a SAN to the configuration re-signs nothing, so the new name stays
// unusable until the certificate expires or the node IP moves.
func TestInvalidateIfStaleExtraSANs(t *testing.T) {
	const nodeIP = "10.0.0.5"

	// withSANs builds a valid cert covering the node IP plus the given extras,
	// and an Embedded configured to want wantSANs.
	withSANs := func(t *testing.T, certIPs []net.IP, certNames []string, wantSANs []string) types.Embedded {
		t.Helper()
		ips := append([]net.IP{net.ParseIP(nodeIP)}, certIPs...)
		cert := selfSignedCertWithNames(t, time.Now().Add(24*time.Hour), ips, certNames)
		e := writeAPIServerCert(t, nodeIP, cert)
		e.APIServerExtraSANs = wantSANs
		return e
	}

	certKept := func(t *testing.T, e types.Embedded) {
		t.Helper()
		assert.FileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"))
	}
	certRegenerated := func(t *testing.T, e types.Embedded) {
		t.Helper()
		assert.NoFileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"), "stale leaf cert must be removed")
		assert.FileExists(t, filepath.Join(e.PKIDir, "ca", "ca.crt"), "CA must be preserved")
	}

	t.Run("no extra SANs configured is a no-op", func(t *testing.T) {
		e := withSANs(t, nil, nil, nil)
		require.NoError(t, InvalidateIfStale(e))
		certKept(t, e)
	})

	t.Run("cert covers every configured SAN", func(t *testing.T) {
		e := withSANs(t,
			[]net.IP{net.ParseIP("10.0.0.4")},
			[]string{"kubesolo.local"},
			[]string{"10.0.0.4", "kubesolo.local"})
		require.NoError(t, InvalidateIfStale(e))
		certKept(t, e)
	})

	t.Run("newly configured DNS SAN regenerates", func(t *testing.T) {
		e := withSANs(t, nil, nil, []string{"kubesolo.local"})
		require.NoError(t, InvalidateIfStale(e))
		certRegenerated(t, e)
	})

	t.Run("newly configured IP SAN regenerates", func(t *testing.T) {
		e := withSANs(t, nil, nil, []string{"10.0.0.4"})
		require.NoError(t, InvalidateIfStale(e))
		certRegenerated(t, e)
	})

	t.Run("one of several SANs missing regenerates", func(t *testing.T) {
		e := withSANs(t, nil, []string{"kubesolo.local"}, []string{"kubesolo.local", "other.local"})
		require.NoError(t, InvalidateIfStale(e))
		certRegenerated(t, e)
	})

	t.Run("DNS SAN comparison ignores case", func(t *testing.T) {
		// TLS itself matches case-insensitively, so the certificate is already
		// usable and re-signing would achieve nothing.
		e := withSANs(t, nil, []string{"kubesolo.local"}, []string{"KubeSolo.Local"})
		require.NoError(t, InvalidateIfStale(e))
		certKept(t, e)
	})

	t.Run("blank and whitespace SANs are ignored", func(t *testing.T) {
		e := withSANs(t, nil, nil, []string{"", "   "})
		require.NoError(t, InvalidateIfStale(e))
		certKept(t, e)
	})

	// This is the failure mode the classification exists to prevent. addExtraSANs
	// discards a SAN that is neither an IPv4 address nor a DNS name, so it never
	// reaches the certificate. Treating it as missing would destroy and re-sign
	// the leaf certificates on every single boot, forever.
	t.Run("SAN that generation would discard does not regenerate", func(t *testing.T) {
		for _, bad := range []string{"not a san", "http://kubesolo.local", "2001:db8::1"} {
			t.Run(bad, func(t *testing.T) {
				e := withSANs(t, nil, nil, []string{bad})
				require.NoError(t, InvalidateIfStale(e))
				certKept(t, e)
			})
		}
	})

	t.Run("extra SANs are checked even when the node IP is loopback", func(t *testing.T) {
		cert := selfSignedCertWithNames(t, time.Now().Add(24*time.Hour), nil, nil)
		e := writeAPIServerCert(t, "127.0.0.1", cert)
		e.APIServerExtraSANs = []string{"kubesolo.local"}
		require.NoError(t, InvalidateIfStale(e))
		certRegenerated(t, e)
	})
}
