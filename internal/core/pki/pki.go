package pki

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/types"
)

// GenerateAllCertificates creates all certificates needed for the specified component
func GenerateAllCertificates(embedded types.Embedded) error {
	caOpts := defaultCertOptions(CACert, embedded)
	if err := generateCertificate(caOpts); err != nil {
		return fmt.Errorf("failed to generate CA certificate: %v", err)
	}

	kubeletOpts := defaultCertOptions(KubeletCert, embedded)
	if err := generateCertificate(kubeletOpts); err != nil {
		return fmt.Errorf("failed to generate kubelet certificate: %v", err)
	}

	apiserverOpts := defaultCertOptions(APIServerCert, embedded)
	if err := generateCertificate(apiserverOpts); err != nil {
		return fmt.Errorf("failed to generate apiserver certificate: %v", err)
	}

	controllerOpts := defaultCertOptions(ControllerManagerCert, embedded)
	if err := generateCertificate(controllerOpts); err != nil {
		return fmt.Errorf("failed to generate controller-manager certificate: %v", err)
	}

	adminOpts := defaultCertOptions(AdminCert, embedded)
	if err := generateCertificate(adminOpts); err != nil {
		return fmt.Errorf("failed to generate admin certificate: %v", err)
	}

	webhookOpts := defaultCertOptions(WebhookCert, embedded)
	if err := generateCertificate(webhookOpts); err != nil {
		return fmt.Errorf("failed to generate webhook certificate: %v", err)
	}

	return nil
}

// generateCertificate creates a certificate based on the provided options
func generateCertificate(opts CertOptions) error {
	if err := ensureCertificateDirectories(opts); err != nil {
		return err
	}

	if certAlreadyExists(opts.CertDir, opts.KeyDir) {
		return nil
	}

	privateKey, err := generatePrivateKey(opts.KeySize)
	if err != nil {
		return err
	}

	template, _, _, err := createCertificateTemplate(opts)
	if err != nil {
		return err
	}

	cert, err := signCertificate(opts, template, privateKey)
	if err != nil {
		return err
	}

	if err := writeCertificateAndKey(opts.CertDir, opts.KeyDir, cert, privateKey); err != nil {
		return err
	}

	return nil
}

// ensureCertificateDirectories makes sure all required directories exist
func ensureCertificateDirectories(opts CertOptions) error {
	dirs := []string{}

	if opts.CertDir != "" {
		dirs = append(dirs, filepath.Dir(opts.CertDir))
	}
	if opts.KeyDir != "" {
		dirs = append(dirs, filepath.Dir(opts.KeyDir))
	}
	if opts.SignerCertDir != "" {
		dirs = append(dirs, opts.SignerCertDir)
	}
	if opts.SignerKeyDir != "" {
		dirs = append(dirs, opts.SignerKeyDir)
	}

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := filesystem.EnsureDirectoryExists(dir); err != nil {
			return fmt.Errorf("failed to ensure directory exists: %v", err)
		}
	}
	return nil
}

// certAlreadyExists checks if a certificate already exists
func certAlreadyExists(certPath, keyPath string) bool {
	return filesystem.FileExists(certPath) && filesystem.FileExists(keyPath)
}

// generatePrivateKey creates a new private key
func generatePrivateKey(keySize int) (*rsa.PrivateKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, keySize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %v", err)
	}
	return privateKey, nil
}

// createCertificateTemplate creates a certificate template based on options
func createCertificateTemplate(opts CertOptions) (*x509.Certificate, time.Time, time.Time, error) {
	notBefore := time.Now()
	notAfter := notBefore.Add(time.Duration(opts.NotAfterDays) * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("failed to generate serial number: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   opts.CommonName,
			Organization: opts.Organization,
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		DNSNames:              opts.DNSNames,
		IPAddresses:           opts.IPAddresses,
		BasicConstraintsValid: true,
	}

	configureCertificateByType(template, opts.Type)

	return template, notBefore, notAfter, nil
}

// configureCertificateByType sets the appropriate certificate extensions and usage flags
func configureCertificateByType(template *x509.Certificate, certType CertificateType) {
	switch certType {
	case CACert:
		template.IsCA = true
		template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature

	case KubeletCert:
		template.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}

	case APIServerCert:
		template.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}

	case ControllerManagerCert:
		template.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}

	case AdminCert:
		template.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}

	case WebhookCert:
		template.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
}

// signCertificate signs the certificate with the provided private key or CA
func signCertificate(opts CertOptions, template *x509.Certificate, privateKey *rsa.PrivateKey) ([]byte, error) {
	var cert []byte
	var err error

	if opts.Type == CACert {
		cert, err = x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create self-signed certificate: %v", err)
		}
	} else {
		signerCert, signerKey, err := loadCertificateAndKey(opts.SignerCertDir, opts.SignerKeyDir)
		if err != nil {
			return nil, fmt.Errorf("failed to load CA certificate: %v", err)
		}

		cert, err = x509.CreateCertificate(rand.Reader, template, signerCert, &privateKey.PublicKey, signerKey)
		if err != nil {
			return nil, fmt.Errorf("failed to sign certificate: %v", err)
		}
	}

	return cert, nil
}

// writeCertificateAndKey writes certificate and key to disk
func writeCertificateAndKey(certPath, keyPath string, cert []byte, privateKey *rsa.PrivateKey) error {
	certOut, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("failed to open certificate file for writing: %v", err)
	}
	defer certOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: cert}); err != nil {
		return fmt.Errorf("failed to write certificate data: %v", err)
	}

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open key file for writing: %v", err)
	}
	defer keyOut.Close()

	keyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}

	if err := pem.Encode(keyOut, keyBlock); err != nil {
		return fmt.Errorf("failed to write key data: %v", err)
	}

	return nil
}

// loadCertificateAndKey loads certificate and key from disk
func loadCertificateAndKey(certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPEMBlock, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read certificate file: %v", err)
	}

	certDERBlock, _ := pem.Decode(certPEMBlock)
	if certDERBlock == nil {
		return nil, nil, fmt.Errorf("failed to parse certificate PEM data")
	}

	cert, err := x509.ParseCertificate(certDERBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %v", err)
	}

	keyPEMBlock, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read key file: %v", err)
	}

	keyDERBlock, _ := pem.Decode(keyPEMBlock)
	if keyDERBlock == nil {
		return nil, nil, fmt.Errorf("failed to parse key PEM data")
	}

	key, err := x509.ParsePKCS1PrivateKey(keyDERBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse key: %v", err)
	}

	return cert, key, nil
}
