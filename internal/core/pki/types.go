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
	// RequestHeaderCACert is the request header CA certificate
	RequestHeaderCACert CertificateType = "request-header-ca"
	// RequestHeaderClientCert is the request header client certificate
	RequestHeaderClientCert CertificateType = "request-header-client"
	// D2KServerCert is the TLS server certificate for the d2k Docker-compatible
	// API endpoint, signed by the kubesolo CA.
	D2KServerCert CertificateType = "d2k-server"
	// D2KClientCert is the TLS client certificate operators present to the d2k
	// endpoint via `docker --tlscert/--tlskey`, signed by the kubesolo CA.
	D2KClientCert CertificateType = "d2k-client"
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
