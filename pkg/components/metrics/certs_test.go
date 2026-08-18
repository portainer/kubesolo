package metrics

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// writeTestCert writes a self-signed certificate valid over [notBefore, notAfter]
// to a file in dir and returns its path.
func writeTestCert(t *testing.T, dir, name string, notBefore, notAfter time.Time) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	path := filepath.Join(dir, name+".crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	return path
}

func TestCertificateCollector(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	expiry := now.Add(24 * time.Hour).Truncate(time.Second)
	validPath := writeTestCert(t, dir, "valid", now.Add(-time.Hour), expiry)
	expiredPath := writeTestCert(t, dir, "expired", now.Add(-48*time.Hour), now.Add(-24*time.Hour))

	corruptPath := filepath.Join(dir, "corrupt.crt")
	if err := os.WriteFile(corruptPath, []byte("not a pem file"), 0600); err != nil {
		t.Fatalf("write corrupt certificate: %v", err)
	}

	c := &certificateCollector{
		certs: []trackedCertificate{
			{"valid", validPath},
			{"expired", expiredPath},
			{"corrupt", corruptPath},
			{"missing", filepath.Join(dir, "does-not-exist.crt")},
		},
		expiry: prometheus.NewDesc(
			"kubesolo_certificate_expiry_timestamp_seconds", "help", []string{"name"}, nil,
		),
		valid: prometheus.NewDesc(
			"kubesolo_certificate_valid", "help", []string{"name"}, nil,
		),
	}

	if got := testutil.CollectAndCount(c); got != 6 {
		t.Errorf("expected 6 metrics (2 for valid, 2 for expired, 1 each for corrupt/missing), got %d", got)
	}

	expected := strings.NewReader(`
# HELP kubesolo_certificate_valid help
# TYPE kubesolo_certificate_valid gauge
kubesolo_certificate_valid{name="valid"} 1
kubesolo_certificate_valid{name="expired"} 0
kubesolo_certificate_valid{name="corrupt"} 0
kubesolo_certificate_valid{name="missing"} 0
`)
	if err := testutil.CollectAndCompare(c, expected, "kubesolo_certificate_valid"); err != nil {
		t.Errorf("certificate_valid mismatch: %v", err)
	}

	// The expiry series must be present for parseable certs (whether or not they
	// are still in their validity window) and absent for unreadable ones.
	expectedExpiry := strings.NewReader(`
# HELP kubesolo_certificate_expiry_timestamp_seconds help
# TYPE kubesolo_certificate_expiry_timestamp_seconds gauge
kubesolo_certificate_expiry_timestamp_seconds{name="valid"} ` + strconv.FormatInt(expiry.Unix(), 10) + `
kubesolo_certificate_expiry_timestamp_seconds{name="expired"} ` + strconv.FormatInt(now.Add(-24*time.Hour).Truncate(time.Second).Unix(), 10) + `
`)
	if err := testutil.CollectAndCompare(c, expectedExpiry, "kubesolo_certificate_expiry_timestamp_seconds"); err != nil {
		t.Errorf("certificate_expiry mismatch: %v", err)
	}
}

func TestParseCertificateFileSkipsNonCertificateBlocks(t *testing.T) {
	dir := t.TempDir()
	certPath := writeTestCert(t, dir, "leaf", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}

	// Prepend a non-CERTIFICATE PEM block; the collector must skip past it.
	bundlePath := filepath.Join(dir, "bundle.crt")
	bundle := append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("junk")}), certPEM...)
	if err := os.WriteFile(bundlePath, bundle, 0600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	cert, err := parseCertificateFile(bundlePath)
	if err != nil {
		t.Fatalf("parseCertificateFile: %v", err)
	}
	if cert.Subject.CommonName != "leaf" {
		t.Errorf("expected leaf certificate, got CN %q", cert.Subject.CommonName)
	}
}
