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

// RequestHeaderCertificatePaths defines paths for request header CA and client certificates
type RequestHeaderCertificatePaths struct {
	CACert     string
	CAKey      string
	ClientCert string
	ClientKey  string
}

// D2KCertificatePaths defines paths for the d2k TLS material.
// CACert is the kubesolo CA certificate that signs both server and client certs;
// it is the existing /var/lib/kubesolo/pki/ca/ca.crt and is referenced by docker
// CLI clients via --tlscacert. ServerCert/ServerKey are mounted into the d2k pod
// as /etc/d2k/tls/tls.crt and tls.key. ClientCert/ClientKey are used by docker
// CLI clients via --tlscert and --tlskey.
type D2KCertificatePaths struct {
	CACert     string
	ServerCert string
	ServerKey  string
	ClientCert string
	ClientKey  string
}

type Embedded struct {
	// System Node IP
	NodeIP string

	// NodeIPSpecified is true when the node IP was explicitly set via --node-ip
	// (rather than auto-detected). When set, the API server cert SANs are scoped
	// to this IP instead of every local interface address.
	NodeIPSpecified bool

	// Network MTU used by the embedded CNI bridge (cni0) and pod veth interfaces
	MTU int

	// MTUSpecified is true when the MTU was explicitly set via --mtu (rather
	// than auto-detected).
	MTUSpecified bool

	// PKI directories
	PKIDir              string
	PKICADir            string
	PKIAdminDir         string
	PKIAPIServerDir     string
	PKIControllerDir    string
	PKIKubeletDir       string
	PKIWebhookDir       string
	PKIRequestHeaderDir string

	// Admin kubeconfig file
	AdminKubeconfigFile string

	// Certificate paths
	KubeletCerts           KubeletCertificatePaths
	APIServerCerts         APIServerCertificatePaths
	ControllerManagerCerts ControllerManagerCertificatePaths
	AdminCerts             AdminCertificatePaths
	WebhookCerts           WebhookCertificatePaths
	CACerts                CACertificatePaths
	RequestHeaderCerts     RequestHeaderCertificatePaths

	// Containerd directories and files
	ContainerdDir               string
	ContainerdSocketFile        string
	ContainerdBinaryFile        string
	ContainerdImagesDir         string
	ContainerdConfigFile        string
	ContainerdShimBinaryFile    string
	ContainerdRootDir           string
	ContainerdStateDir          string
	ContainerdRegistryConfigDir string

	// Conitainerd CNI directories and files
	ContainerdCNIDir        string
	ContainerdCNIPluginsDir string
	ContainerdCNIConfigDir  string
	ContainerdCNIConfigFile string

	// Crun binary
	CrunBinaryFile string

	// Container runtime. RuntimeExternal is true when KubeSolo attaches to a
	// host-managed CRI runtime given with --container-runtime-endpoint instead of
	// starting its own embedded containerd. RuntimeEndpoint is populated in both
	// cases — for the embedded containerd it is "unix://" + ContainerdSocketFile —
	// so consumers need only one code path. RuntimeSocketPath is the filesystem
	// path of RuntimeEndpoint.
	// RuntimeCgroupDriver is the cgroup driver reported by an external runtime over
	// CRI, which the kubelet must match. Empty when the runtime does not report one,
	// in which case the kubelet detects the driver from the host instead.
	RuntimeExternal     bool
	RuntimeEndpoint     string
	RuntimeSocketPath   string
	RuntimeCgroupDriver string

	// Kubelet directories
	KubeletDir            string
	KubeletConfigDir      string
	KubeletConfigFile     string
	KubeletKubeConfigFile string
	KubeletPluginsDir     string

	// API Server directory
	APIServerDir          string
	ServiceAccountKeyFile string
	// API Server extra SANs
	APIServerExtraSANs []string

	// Kine directories and files
	KineDir        string
	KineSocketFile string

	// Controller manager directory
	ControllerDir string

	// Webhook directory
	WebhookDir string

	// Images
	PortainerEdgeImageFile        string
	CorednsImageFile              string
	SandboxImageFile              string
	LocalPathProvisionerImageFile string
	D2KImageFile                  string

	// Load Balancer
	LoadBalancer bool

	// LoadBalancerIP is the IP published as the LoadBalancer EXTERNAL-IP.
	// Defaults to NodeIP unless overridden with --load-balancer-ip.
	LoadBalancerIP string

	// Local Path Storage
	LocalPathStorageDir string

	// Portainer Edge
	IsPortainerEdge    bool
	PortainerEdgeImage string

	// Container Mode
	ContainerMode bool

	// IPv6
	DisableIPv6 bool

	// d2k integration
	D2K          bool
	D2KNamespace string
	D2KCerts     D2KCertificatePaths
}

// EdgeAgentConfig contains configuration for Portainer Edge Agent
type EdgeAgentConfig struct {
	Image            string
	EdgeID           string
	EdgeKey          string
	EdgeAsync        bool
	EdgeInsecurePoll string
	EdgeSecret       string
	EnvVars          map[string]string
}
