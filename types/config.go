package types

// Config is the KubeSolo configuration document, read from
// DefaultConfigFile and served by the config API.
//
// It carries only settings a user supplies. Everything KubeSolo derives from
// them — the certificate, containerd, kine and kubelet paths — lives on
// Embedded, which is built from a Config rather than replaced by it.
//
// Fields are tagged for JSON only. sigs.k8s.io/yaml converts YAML to JSON
// before unmarshalling, so one set of tags serves both the file on disk and the
// API payload; a second set of yaml tags would be a second thing to keep in
// sync.
const (
	// ConfigAPIVersion versions the document schema. A file declaring an
	// apiVersion KubeSolo does not recognise is rejected rather than guessed at.
	ConfigAPIVersion = "kubesolo.io/v1alpha1"
	ConfigKind       = "Config"
)

type Config struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	// Path is the directory KubeSolo stores all of its state in. It cannot be
	// changed on a running install: every certificate, the kine database and all
	// containerd state live below it, and nothing migrates them.
	Path string `json:"path,omitempty"`

	PKI        PKIConfig        `json:"pki"`
	Logging    LoggingConfig    `json:"logging"`
	Network    NetworkConfig    `json:"network"`
	Runtime    RuntimeConfig    `json:"runtime"`
	Kubernetes KubernetesConfig `json:"kubernetes"`
	Storage    StorageConfig    `json:"storage"`
	Portainer  PortainerConfig  `json:"portainer"`
	D2K        D2KConfig        `json:"d2k"`
	Metrics    MetricsConfig    `json:"metrics"`
	API        ConfigAPI        `json:"api"`
}

// PKIConfig supplies certificate material KubeSolo would otherwise generate.
//
// This exists for clusters whose trust anchor is owned by something else. Talos
// holds the Kubernetes CA and hands the same CA to its kubelet, so KubeSolo has
// to sign with that CA or the two never trust each other.
//
// The files are read where they are, never copied into the KubeSolo PKI
// directory. That keeps them out of reach of the leaf-certificate regeneration
// that runs when the node IP moves, and lets them stay read-only and owned by
// whoever provisioned them.
type PKIConfig struct {
	// CACert and CAKey are the Kubernetes root CA. Both or neither: KubeSolo
	// signs with this CA, so a certificate without its key is unusable.
	// Empty means KubeSolo generates and owns the CA, which is the default.
	CACert string `json:"caCert,omitempty"`
	CAKey  string `json:"caKey,omitempty"`
}

// LoggingConfig controls log verbosity and the pprof server.
type LoggingConfig struct {
	Debug bool `json:"debug"`
	Pprof bool `json:"pprof"`
}

// NetworkConfig covers node addressing and the embedded CNI bridge.
type NetworkConfig struct {
	// NodeIP overrides the auto-detected node IP. Empty means auto-detect,
	// preferring a private (RFC 1918) address.
	NodeIP string `json:"nodeIP"`

	// MTU overrides the auto-detected MTU used by cni0 and pod veth interfaces.
	// Zero means auto-detect.
	MTU int `json:"mtu"`

	// DisableIPv6 stops CoreDNS serving ip6.arpa reverse zones and makes the
	// kubelet register with an explicit IPv4 node address.
	DisableIPv6  bool               `json:"disableIPv6"`
	LoadBalancer LoadBalancerConfig `json:"loadBalancer"`
}

// LoadBalancerConfig controls the EXTERNAL-IP KubeSolo sets on Services of type
// LoadBalancer.
type LoadBalancerConfig struct {
	Enabled bool `json:"enabled"`

	// IP overrides the address published as EXTERNAL-IP. Empty means the node IP.
	IP string `json:"ip"`
}

// RuntimeConfig selects the container runtime.
type RuntimeConfig struct {
	// Endpoint is the CRI endpoint of a host-managed runtime, for example
	// unix:///run/crio/crio.sock. Empty means KubeSolo runs its own containerd.
	Endpoint string `json:"endpoint"`

	// ContainerMode forces container mode on or off. Nil — the default — means
	// auto-detect, which is why this is a pointer: false and unset must differ.
	ContainerMode *bool `json:"containerMode,omitempty"`
}

// KubernetesConfig groups the Kubernetes control plane components.
//
// These are grouped because the family grows: controller-manager, kube-proxy,
// CoreDNS and the pod/service CIDRs are still fixed in const.go and are the
// likeliest settings to become configurable next. Everything else in this
// document is single-purpose and stays at the top level.
type KubernetesConfig struct {
	// NodeName is the name of the single node this control plane manages. Empty
	// means the hostname.
	//
	// It is trimmed and lowercased, because that is what the kubelet does to the
	// name before registering it, and the NodeSetter webhook pins every pod to
	// this name — a mismatch leaves the whole cluster Pending.
	NodeName string `json:"nodeName,omitempty"`

	// BootstrapToken turns on Kubernetes TLS bootstrapping, in the standard
	// "<6 chars>.<16 chars>" form. Empty — the default — leaves it off.
	//
	// KubeSolo's own kubelet does not need this: it is handed a client
	// certificate KubeSolo has already signed. A kubelet KubeSolo does not
	// control has no such option — Talos, for one, only ever enrols by
	// presenting a bootstrap token and requesting a certificate — so setting
	// this enables bootstrap-token authentication on the API server, starts the
	// controller manager's CSR signers, and seeds the token Secret and the RBAC
	// that lets node client CSRs be approved automatically.
	//
	// It is a credential: anything holding it can obtain a node certificate for
	// this cluster.
	BootstrapToken string `json:"bootstrapToken,omitempty"`

	APIServer APIServerConfig `json:"apiServer"`
	Kubelet   KubeletConfig   `json:"kubelet"`
}

// APIServerConfig covers the Kubernetes API server.
type APIServerConfig struct {
	// ExtraSANs are additional Subject Alternative Names for the API server
	// certificate, as IP addresses or DNS names.
	ExtraSANs []string `json:"extraSANs,omitempty"`

	// StartupTimeoutSeconds bounds how long each component may take to pass its
	// health check at startup. Raise it on slow storage such as SD cards.
	StartupTimeoutSeconds int `json:"startupTimeoutSeconds"`
}

// KubeletConfig covers the node agent's resource management.
type KubeletConfig struct {
	// External attaches KubeSolo to a kubelet the host already runs instead of
	// starting one, the same split Runtime.Endpoint makes for the container
	// runtime. KubeSolo then supervises no kubelet process: it waits for the
	// host's kubelet to register NodeName with its API server, and treats that
	// registration as the readiness signal.
	//
	// The remaining settings in this struct configure the kubelet KubeSolo
	// starts, so they have no effect when this is true.
	External bool `json:"external"`

	CPUManager CPUManagerConfig `json:"cpuManager"`

	// SystemReserved is withheld from node allocatable for the host, keyed by
	// resource name, e.g. {"cpu": "1", "memory": "500Mi"}.
	SystemReserved map[string]string `json:"systemReserved,omitempty"`
}

// StorageConfig covers the local-path provisioner and the kine database.
type StorageConfig struct {
	LocalPath LocalPathConfig `json:"localPath"`

	// DBWALRepair runs an integrity check against the SQLite database at startup
	// and clears WAL artefacts if it is corrupt. Recovers from power loss.
	DBWALRepair bool `json:"dbWALRepair"`
}

// LocalPathConfig covers the local-path storage provisioner.
type LocalPathConfig struct {
	Enabled bool `json:"enabled"`

	// SharedPath is a shared filesystem for local storage. Empty means the
	// provisioner uses its directory below Path.
	SharedPath string `json:"sharedPath"`
}

// PortainerConfig covers the optional Portainer Edge Agent.
type PortainerConfig struct {
	EdgeID string `json:"edgeID"`

	// EdgeKey is a credential. It is redacted in config API responses and the
	// config file is written 0600.
	EdgeKey string `json:"edgeKey"`
	Async   bool   `json:"async"`

	// Image is the full agent image reference including its tag. Anything other
	// than the default is pulled from a registry rather than loaded from the
	// embedded archive.
	Image string `json:"image"`
}

// D2KConfig covers the Docker-to-Kubernetes API translator.
type D2KConfig struct {
	Enabled bool `json:"enabled"`

	// Namespace is the single namespace d2k is deployed into and translates
	// Docker API calls against.
	Namespace string `json:"namespace"`
}

// ConfigAPI covers the config API itself.
//
// The API listens on a unix socket rather than a port: the filesystem
// permissions on the socket are the authorisation model, which avoids putting a
// mutating endpoint on the network of an edge device.
type ConfigAPI struct {
	Enabled bool `json:"enabled"`

	// SocketPath is where the socket is created. Empty means
	// <path>/DefaultAPISocketName.
	SocketPath string `json:"socketPath"`
}
