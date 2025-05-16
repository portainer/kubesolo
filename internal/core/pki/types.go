package pki

import (
	"net"
)

// CertificateType defines what type of certificate to generate
type CertificateType string

const (
	// CACert is a certificate authority
	CACert CertificateType = "ca"
	// KubeletCert is for the kubelet
	KubeletCert CertificateType = "kubelet"
	// APIServerCert is for the API server
	APIServerCert CertificateType = "apiserver"
	// ControllerManagerCert is for the kube-controller-manager
	ControllerManagerCert CertificateType = "controller-manager"
	// AdminCert is for admin users
	AdminCert CertificateType = "admin"
	// WebhookCert is for the webhook
	WebhookCert CertificateType = "webhook"
)

// CertOptions holds configuration for certificate generation
type CertOptions struct {
	// Type of certificate to generate
	Type CertificateType

	// Basic information
	CommonName   string
	Organization []string

	// Subject Alternative Names
	DNSNames    []string
	IPAddresses []net.IP

	// Certificate properties
	NotAfterDays int
	KeySize      int // RSA key size in bits

	// For signed certificates
	SignerCertDir string
	SignerKeyDir  string

	// Output Dirs
	CertDir string
	KeyDir  string
}

// CertificateDirs holds Dirs to certificate files
type CertificateDirs struct {
	CertDir   string
	KeyDir    string
	CACertDir string
}
