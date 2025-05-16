package types

// CertificatePaths defines the paths for a specific certificate type
type CertificatePaths struct {
	CACert string
	Cert   string
	Key    string
}

// CACertificatePaths defines paths for CA certificates
type CACertificatePaths struct {
	Cert string
	Key  string
}

// KubeletCertificatePaths defines paths for kubelet certificates
type KubeletCertificatePaths struct {
	CertificatePaths
}

// APIServerCertificatePaths defines paths for API server certificates
type APIServerCertificatePaths struct {
	CertificatePaths
}

// ControllerManagerCertificatePaths defines paths for controller manager certificates
type ControllerManagerCertificatePaths struct {
	CertificatePaths
}

// AdminCertificatePaths defines paths for admin certificates
type AdminCertificatePaths struct {
	CertificatePaths
}

// WebhookCertificatePaths defines paths for webhook certificates
type WebhookCertificatePaths struct {
	CertificatePaths
}

type Embedded struct {
	// System paths (not managed by kubesolo)
	SystemCNIDir string

	// PKI directories
	PKIDir           string
	PKICADir         string
	PKIAdminDir      string
	PKIAPIServerDir  string
	PKIControllerDir string
	PKIKubeletDir    string
	PKIWebhookDir    string

	// Admin kubeconfig file
	AdminKubeconfigFile string

	// Certificate paths
	KubeletCerts           KubeletCertificatePaths
	APIServerCerts         APIServerCertificatePaths
	ControllerManagerCerts ControllerManagerCertificatePaths
	AdminCerts             AdminCertificatePaths
	WebhookCerts           WebhookCertificatePaths
	CACerts                CACertificatePaths

	// Containerd directories and files
	ContainerdDir            string
	ContainerdSocketFile     string
	ContainerdBinaryFile     string
	ContainerdImagesDir      string
	ContainerdConfigFile     string
	ContainerdShimBinaryFile string
	ContainerdRootDir        string
	ContainerdStateDir       string
	// Conitainerd CNI directories and files
	ContainerdCNIDir        string
	ContainerdCNIPluginsDir string
	ContainerdCNIConfigDir  string
	ContainerdCNIConfigFile string

	// Runc binary
	RuncBinaryFile string

	// Kubelet directories
	KubeletDir            string
	KubeletConfigDir      string
	KubeletConfigFile     string
	KubeletKubeConfigFile string
	KubeletPluginsDir     string

	// API Server directory
	APIServerDir          string
	ServiceAccountKeyFile string

	// Kine directories and files
	KineDir        string
	KineSocketFile string

	// Controller manager directory
	ControllerDir string

	// Webhook directory
	WebhookDir string

	// Images
	PortainerAgentImageFile string
	CorednsImageFile        string

	// Portainer Edge
	IsPortainerEdge bool
}

// EdgeAgentConfig contains configuration for Portainer Edge Agent
type EdgeAgentConfig struct {
	EdgeID           string
	EdgeKey          string
	EdgeInsecurePoll string
	EdgeSecret       string
	EnvVars          map[string]string
}
