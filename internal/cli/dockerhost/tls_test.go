package dockerhost

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type keyPair struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
	keyPEM  []byte
}

func issue(t *testing.T, tmpl *x509.Certificate, parent *keyPair) *keyPair {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return &keyPair{cert, key,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})}
}

// A context for an engine that requires TLS client authentication, as a remote
// docker daemon does, must reach it with the material stored with the context.
func TestNewClientUsesContextTLS(t *testing.T) {
	now := time.Now()
	ca := issue(t, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"},
		NotBefore: now, NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}, nil)
	server := issue(t, &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "server"},
		NotBefore: now, NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}, ca)
	client := issue(t, &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "client"},
		NotBefore: now, NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}, ca)

	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	pair, _ := tls.X509KeyPair(server.certPEM, server.keyPEM)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.45")
		_, _ = w.Write([]byte("OK"))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}
	srv.StartTLS()
	defer srv.Close()

	dir := setup(t, "remote")
	writeContext(t, dir, "remote", "tcp://"+strings.TrimPrefix(srv.URL, "https://"))
	sum := sha256.Sum256([]byte("remote"))
	tlsDir := filepath.Join(dir, "contexts", "tls", hex.EncodeToString(sum[:]), "docker")
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for file, data := range map[string][]byte{"ca.pem": ca.certPEM, "cert.pem": client.certPEM, "key.pem": client.keyPEM} {
		if err := os.WriteFile(filepath.Join(tlsDir, file), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The DOCKER_HOST environment must not leak into a context.
	t.Setenv("DOCKER_TLS_VERIFY", "1")
	t.Setenv("DOCKER_CERT_PATH", filepath.Join(dir, "nowhere"))

	cli, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cli.Close() }()
	if _, err := cli.Ping(context.Background()); err != nil {
		t.Fatalf("ping over the context's TLS: %v", err)
	}

	// Without the client certificate the engine refuses the connection.
	if err := os.Remove(filepath.Join(tlsDir, "cert.pem")); err != nil {
		t.Fatal(err)
	}
	cli2, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cli2.Close() }()
	if _, err := cli2.Ping(context.Background()); err == nil {
		t.Error("ping without a client certificate succeeded; the context's TLS material is not what was used")
	}
}
