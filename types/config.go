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

	// PprofPort is the port the pprof server listens on, on every interface,
	// when Pprof is set.
	PprofPort int `json:"pprofPort"`
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
// These are grouped because the family grows. Everything else in this document
// is single-purpose and stays at the top level.
type KubernetesConfig struct {
	// NodeName is the name of the single node this control plane manages. Empty
	// means the hostname.
	//
	// It is trimmed and lowercased, because that is what the kubelet does to the
	// name before registering it, and the NodeSetter webhook pins every pod to
	// this name — a mismatch leaves the whole cluster Pending.
	NodeName string `json:"nodeName,omitempty"`

	// PodCIDR is the range pod IPs are allocated from: the CNI bridge's, the
	// node's spec.podCIDR, kube-proxy's and the pod masquerade rule's. It cannot
	// be changed on an existing install: the node's spec.podCIDR is immutable
	// once allocated, and running pods keep their addresses.
	PodCIDR string `json:"podCIDR"`

	// ServiceCIDR is the range Service cluster IPs are allocated from. Its first
	// address is the kubernetes Service's, which the API server certificate
	// carries. It cannot be changed on an existing install: Services keep the
	// cluster IPs they were given.
	ServiceCIDR string `json:"serviceCIDR"`

	// ClusterDNS is the cluster IP of the CoreDNS Service, which the kubelet hands
	// to every pod as its nameserver. Empty means the tenth address of
	// ServiceCIDR. Like it, it cannot be changed on an existing install.
	ClusterDNS string `json:"clusterDNS"`

	APIServer         APIServerConfig         `json:"apiServer"`
	ControllerManager ControllerManagerConfig `json:"controllerManager"`
	Kubelet           KubeletConfig           `json:"kubelet"`
	KubeProxy         KubeProxyConfig         `json:"kubeProxy"`
	Webhook           WebhookConfig           `json:"webhook"`
}

// APIServerConfig covers the Kubernetes API server.
type APIServerConfig struct {
	// ExtraSANs are additional Subject Alternative Names for the API server
	// certificate, as IP addresses or DNS names.
	ExtraSANs []string `json:"extraSANs,omitempty"`

	// StartupTimeoutSeconds bounds how long each component may take to pass its
	// health check at startup. Raise it on slow storage such as SD cards.
	StartupTimeoutSeconds int `json:"startupTimeoutSeconds"`

	// Port is the API server's secure port, on every interface. The generated
	// kubeconfigs point at it.
	Port int `json:"port"`
}

// ControllerManagerConfig covers the Kubernetes controller manager.
type ControllerManagerConfig struct {
	// Port is the controller manager's secure (health and metrics) port, on
	// every interface.
	Port int `json:"port"`
}

// KubeletConfig covers the node agent's resource management.
type KubeletConfig struct {
	CPUManager CPUManagerConfig `json:"cpuManager"`

	// SystemReserved is withheld from node allocatable for the host, keyed by
	// resource name, e.g. {"cpu": "1", "memory": "500Mi"}.
	SystemReserved map[string]string `json:"systemReserved,omitempty"`

	// Port is the kubelet API's port, on every interface: the API server reaches
	// it for logs, exec and port-forward.
	Port int `json:"port"`

	// HealthzPort is the kubelet's health endpoint, on loopback.
	HealthzPort int `json:"healthzPort"`
}

// KubeProxyConfig covers kube-proxy.
type KubeProxyConfig struct {
	// HealthzPort is kube-proxy's health endpoint, on every interface.
	HealthzPort int `json:"healthzPort"`
}

// WebhookConfig covers KubeSolo's admission webhook, which pins every pod to
// the node (KubeSolo runs no scheduler) and publishes LoadBalancer IPs.
type WebhookConfig struct {
	// Port is the webhook's port, on loopback: only the API server calls it.
	Port int `json:"port"`
}

// StorageConfig covers the local-path provisioner and the kine database.
type StorageConfig struct {
	LocalPath LocalPathConfig `json:"localPath"`
	Kine      KineConfig      `json:"kine"`

	// DBWALRepair runs an integrity check against the SQLite database at startup
	// and clears WAL artefacts if it is corrupt. Recovers from power loss.
	DBWALRepair bool `json:"dbWALRepair"`
}

// KineConfig covers kine, the etcd API in front of the SQLite database.
type KineConfig struct {
	// Port is the port kine serves the etcd API on, on loopback: only the API
	// server talks to it.
	Port int `json:"port"`
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

	// Port is the port the d2k LoadBalancer Service publishes the Docker API on,
	// at the LoadBalancer IP.
	Port int `json:"port"`
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
