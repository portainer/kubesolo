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

	t.Run("refuses a symlinked request-header directory", func(t *testing.T) {
		target := t.TempDir()
		for _, name := range []string{"request-header-client.crt", "request-header-client.key"} {
			require.NoError(t, os.WriteFile(filepath.Join(target, name), nil, 0o600))
		}
		dir := filepath.Join(t.TempDir(), "pki")
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "apiserver"), 0o755))
		require.NoError(t, os.Symlink(target, filepath.Join(dir, "request-header")))

		assert.Error(t, removeLeafCerts(dir), "a symlinked request-header dir must be refused")
		assert.FileExists(t, filepath.Join(target, "request-header-client.crt"), "files outside the PKI dir must be kept")
		assert.FileExists(t, filepath.Join(target, "request-header-client.key"), "files outside the PKI dir must be kept")
		assert.DirExists(t, filepath.Join(dir, "apiserver"), "nothing may be removed before refusing")
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

// The kubernetes Service IP moves with kubernetes.serviceCIDR, which can change
// until the cluster is created: after the certificates, before the database.
func TestInvalidateIfStaleServiceIP(t *testing.T) {
	cert := selfSignedCert(t, time.Now().Add(24*time.Hour), []net.IP{net.ParseIP("10.0.0.5"), net.ParseIP("10.43.0.1")})

	e := writeAPIServerCert(t, "10.0.0.5", cert)
	e.KubernetesServiceIP = "10.43.0.1"
	require.NoError(t, InvalidateIfStale(e))
	assert.FileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"), "a cert carrying the Service IP must be kept")

	e = writeAPIServerCert(t, "10.0.0.5", cert)
	e.KubernetesServiceIP = "10.51.0.1"
	require.NoError(t, InvalidateIfStale(e))
	assert.NoFileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"), "a cert without the Service IP must be removed")
	assert.FileExists(t, filepath.Join(e.PKIDir, "ca", "ca.crt"), "CA must be preserved")
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

// The request-header client cert is the apiserver's proxy client cert for
// aggregated APIs such as metrics-server. It sits next to the request-header CA
// and can expire while apiserver.crt is still valid.
func TestInvalidateIfStaleRequestHeaderClientCert(t *testing.T) {
	// withClientCert returns an Embedded with a valid apiserver.crt, a real
	// request-header CA and the given client cert.
	withClientCert := func(t *testing.T, clientCert []byte) types.Embedded {
		t.Helper()
		e := writeAPIServerCert(t, "10.0.0.5", selfSignedCert(t, time.Now().Add(24*time.Hour), []net.IP{net.ParseIP("10.0.0.5")}))
		dir := filepath.Join(e.PKIDir, "request-header")
		e.RequestHeaderCerts = types.RequestHeaderCertificatePaths{
			CACert:     filepath.Join(dir, "request-header-ca.crt"),
			CAKey:      filepath.Join(dir, "request-header-ca.key"),
			ClientCert: filepath.Join(dir, "request-header-client.crt"),
			ClientKey:  filepath.Join(dir, "request-header-client.key"),
		}
		require.NoError(t, generateCertificate(defaultCertOptions(RequestHeaderCACert, e)))
		require.NoError(t, os.WriteFile(e.RequestHeaderCerts.ClientCert, clientCert, 0o644))
		require.NoError(t, os.WriteFile(e.RequestHeaderCerts.ClientKey, []byte("key"), 0o600))
		return e
	}

	assertCAsKept := func(t *testing.T, e types.Embedded) {
		t.Helper()
		assert.FileExists(t, e.RequestHeaderCerts.CACert, "request-header CA must be preserved")
		assert.FileExists(t, e.RequestHeaderCerts.CAKey, "request-header CA must be preserved")
		assert.FileExists(t, filepath.Join(e.PKIDir, "ca", "ca.crt"), "CA must be preserved")
	}

	t.Run("expired client cert is re-signed by the existing CA", func(t *testing.T) {
		e := withClientCert(t, selfSignedCert(t, time.Now().Add(-time.Hour), nil))
		ca, _, err := loadCertificateAndKey(e.RequestHeaderCerts.CACert, e.RequestHeaderCerts.CAKey)
		require.NoError(t, err)

		require.NoError(t, InvalidateIfStale(e))
		require.NoFileExists(t, e.RequestHeaderCerts.ClientCert, "expired client cert must be removed")
		assert.NoFileExists(t, e.RequestHeaderCerts.ClientKey, "its key must be removed with it")
		assert.FileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"), "the other leaf certs must be kept")
		assertCAsKept(t, e)

		require.NoError(t, generateCertificate(defaultCertOptions(RequestHeaderClientCert, e)))
		client, _, err := loadCertificateAndKey(e.RequestHeaderCerts.ClientCert, e.RequestHeaderCerts.ClientKey)
		require.NoError(t, err)
		roots := x509.NewCertPool()
		roots.AddCert(ca)
		_, err = client.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
		assert.NoError(t, err, "the new client cert must chain to the existing request-header CA")
	})

	t.Run("corrupt client cert is removed", func(t *testing.T) {
		for _, corrupt := range [][]byte{
			[]byte("this is not a certificate"),
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not DER")}),
		} {
			e := withClientCert(t, corrupt)
			require.NoError(t, InvalidateIfStale(e))
			assert.NoFileExists(t, e.RequestHeaderCerts.ClientCert, "corrupt client cert must be removed")
			assert.NoFileExists(t, e.RequestHeaderCerts.ClientKey, "its key must be removed with it")
			assertCAsKept(t, e)
		}
	})

	t.Run("valid client cert is kept", func(t *testing.T) {
		e := withClientCert(t, selfSignedCert(t, time.Now().Add(time.Hour), nil))
		require.NoError(t, InvalidateIfStale(e))
		assert.FileExists(t, e.RequestHeaderCerts.ClientCert)
		assert.FileExists(t, e.RequestHeaderCerts.ClientKey)
	})

	t.Run("re-signing the leaf certs replaces the client cert too", func(t *testing.T) {
		e := withClientCert(t, selfSignedCert(t, time.Now().Add(time.Hour), nil))
		e.NodeIP = "192.168.1.10" // not in apiserver.crt, so every leaf cert is re-signed
		require.NoError(t, InvalidateIfStale(e))
		assert.NoFileExists(t, filepath.Join(e.PKIAPIServerDir, "apiserver.crt"))
		assert.NoFileExists(t, e.RequestHeaderCerts.ClientCert, "client cert must be re-signed with the other leaf certs")
		assert.NoFileExists(t, e.RequestHeaderCerts.ClientKey, "client cert must be re-signed with the other leaf certs")
		assertCAsKept(t, e)
	})

	t.Run("checked even when apiserver.crt is missing", func(t *testing.T) {
		e := withClientCert(t, selfSignedCert(t, time.Now().Add(-time.Hour), nil))
		require.NoError(t, os.Remove(filepath.Join(e.PKIAPIServerDir, "apiserver.crt")))
		require.NoError(t, InvalidateIfStale(e))
		assert.NoFileExists(t, e.RequestHeaderCerts.ClientCert)
	})

	t.Run("refuses a symlinked request-header directory", func(t *testing.T) {
		e := withClientCert(t, selfSignedCert(t, time.Now().Add(-time.Hour), nil))
		dir := filepath.Dir(e.RequestHeaderCerts.ClientCert)
		target := filepath.Join(t.TempDir(), "request-header")
		require.NoError(t, os.Rename(dir, target))
		require.NoError(t, os.Symlink(target, dir))

		assert.Error(t, InvalidateIfStale(e), "a symlinked request-header dir must be refused")
		assert.FileExists(t, filepath.Join(target, "request-header-client.crt"), "files outside the PKI dir must be kept")
		assert.FileExists(t, filepath.Join(target, "request-header-client.key"), "files outside the PKI dir must be kept")
	})

	t.Run("refuses a symlinked PKI directory", func(t *testing.T) {
		e := withClientCert(t, selfSignedCert(t, time.Now().Add(-time.Hour), nil))
		target := filepath.Join(t.TempDir(), "pki")
		require.NoError(t, os.Rename(e.PKIDir, target))
		require.NoError(t, os.Symlink(target, e.PKIDir))

		assert.Error(t, InvalidateIfStale(e), "a symlinked PKI dir must be refused")
		assert.FileExists(t, filepath.Join(target, "request-header", "request-header-client.crt"), "files outside the PKI dir must be kept")
		assert.FileExists(t, filepath.Join(target, "request-header", "request-header-client.key"), "files outside the PKI dir must be kept")
	})
}
