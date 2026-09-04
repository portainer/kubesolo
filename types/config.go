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
	// means the hostname, which is what a KubeSolo-managed kubelet registers as.
	//
	// It has to be set when the kubelet is external and registers under a name
	// this host does not share: the NodeSetter webhook pins every pod to this
	// name, so a mismatch leaves the whole cluster Pending.
	NodeName string `json:"nodeName,omitempty"`

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
