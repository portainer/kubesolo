package flags

import "github.com/alecthomas/kingpin/v2"

// the full list of flags for the kubesolo application
// Path is the path to the directory containing the kubesolo configuration files
// APIServerExtraSANs is the flag to add extra SANs to the API Server certificate
// PortainerEdgeID is the Edge ID for the Portainer Edge Agent
// PortainerEdgeKey is the Edge Key for the Portainer Edge Agent that can be used to register the Edge Agent with the Portainer Server
// PortainerEdgeAsync is the flag to enable Portainer Edge Async Mode
// LocalStorage is the flag to enable local storage
// LocalStorageSharedPath is the path to the shared file system for the local storage
// Debug is the flag to enable debug logging
// PprofServer is the flag to enable the pprof server
var (
	Application            = kingpin.New("kubesolo", "Ultra-lightweight, OCI-compliant, single-node Kubernetes built for constrained environments such as IoT or IIoT devices running in embedded environments.")
	Version                = Application.Flag("version", "Show the version and exit.").Short('v').Bool()
	Path                   = Application.Flag("path", "Path to the directory containing the kubesolo configuration files. Defaults to /var/lib/kubesolo.").Envar("KUBESOLO_PATH").Default("/var/lib/kubesolo").String()
	APIServerExtraSANs     = Application.Flag("apiserver-extra-sans", "A comma-separated list of additional Subject Alternative Names (SANs) to include in the API server's TLS certificate. These SANs can be IP addresses or DNS names (e.g., 10.0.0.4,kubesolo.local).").Envar("KUBESOLO_APISERVER_EXTRA_SANS").Default("").String()
	NodeIP                 = Application.Flag("node-ip", "Override the auto-detected node IP. This IP is used for the API server advertise address, the generated kubeconfig, the kubelet node IP and the LoadBalancer EXTERNAL-IP. Useful on hosts with multiple NICs where auto-detection is ambiguous. When unset, kubesolo auto-detects and prefers a private (RFC 1918) address.").Envar("KUBESOLO_NODE_IP").Default("").String()
	PortainerEdgeID        = Application.Flag("portainer-edge-id", "Portainer Edge ID. Defaults to empty string.").Envar("KUBESOLO_PORTAINER_EDGE_ID").Default("").String()
	PortainerEdgeKey       = Application.Flag("portainer-edge-key", "Portainer Edge Key. Defaults to empty string.").Envar("KUBESOLO_PORTAINER_EDGE_KEY").Default("").String()
	PortainerEdgeAsync     = Application.Flag("portainer-edge-async", "Enable Portainer Edge Async Mode. Defaults to false.").Envar("KUBESOLO_PORTAINER_EDGE_ASYNC").Default("false").Bool()
	PortainerEdgeImage     = Application.Flag("portainer-edge-image", "Full image reference deployed for the Portainer Edge Agent, including the tag. Any image other than the default is pulled from the registry rather than loaded from the embedded image. Defaults to docker.io/portainer/agent:lts.").Envar("KUBESOLO_PORTAINER_EDGE_IMAGE").Default("docker.io/portainer/agent:lts").String()
	LoadBalancer           = Application.Flag("load-balancer", "Enable load balancer. With this enabled, kubesolo sets the EXTERNAL-IP on services of type LoadBalancer, both when they are created and when an existing service is changed to that type. Defaults to true.").Envar("KUBESOLO_LOAD_BALANCER").Default("true").Bool()
	LoadBalancerIP         = Application.Flag("load-balancer-ip", "Override the IP published as the LoadBalancer EXTERNAL-IP. Useful on hosts with multiple NICs where services should be advertised on a different address than the node IP. When unset, the node IP is used.").Envar("KUBESOLO_LOAD_BALANCER_IP").Default("").String()
	LocalStorage           = Application.Flag("local-storage", "Enable local storage. Defaults to false.").Envar("KUBESOLO_LOCAL_STORAGE").Default("true").Bool()
	LocalStorageSharedPath = Application.Flag("local-storage-shared-path", "Path to the shared file system for the local storage. Defaults to empty string.").Envar("KUBESOLO_LOCAL_STORAGE_SHARED_PATH").Default("").String()
	Debug                  = Application.Flag("debug", "Enable debug logging. Defaults to false.").Envar("KUBESOLO_DEBUG").Default("false").Bool()
	PprofServer            = Application.Flag("pprof-server", "Enable pprof server. Defaults to false.").Envar("KUBESOLO_PPROF_SERVER").Default("false").Bool()
	ContainerMode          = Application.Flag("container-mode", "Run in container mode with cgroupfs driver and relaxed eviction thresholds. Auto-detected when running inside a container.").Envar("KUBESOLO_CONTAINER_MODE").Bool()
	Full                   = Application.Flag("full", "Deprecated: has no effect. KubeSolo always uses upstream Kubernetes defaults. Retained for backwards compatibility and will be removed in a future release.").Envar("KUBESOLO_FULL").Default("false").Bool()
	DBWALRepair            = Application.Flag("db-wal-repair", "On startup, run an integrity check against the SQLite database and remove WAL artefacts (state.db-wal, state.db-shm) if corruption is detected. Recovers from unclean shutdowns caused by power loss. Defaults to false.").Envar("KUBESOLO_DB_WAL_REPAIR").Default("false").Bool()
	DisableIPv6            = Application.Flag("disable-ipv6", "Disable IPv6 support. When set, CoreDNS will not serve ip6.arpa reverse zones and kubelet will register with an explicit IPv4 node address. Defaults to false.").Envar("KUBESOLO_DISABLE_IPV6").Default("false").Bool()
	StartupTimeout         = Application.Flag("startup-timeout", "Maximum time in seconds to wait for each component to pass its health check during startup. Increase on slow storage such as SD cards. Defaults to 600.").Envar("KUBESOLO_STARTUP_TIMEOUT").Default("600").Int()
	D2K                    = Application.Flag("d2k", "Enable d2k integration. When set, kubesolo deploys the Portainer d2k Docker-to-Kubernetes translator into the target namespace and exposes a Docker-compatible API endpoint over mTLS on port 2376. Defaults to false.").Envar("KUBESOLO_D2K").Default("false").Bool()
	D2KNamespace           = Application.Flag("d2k-namespace", "Single namespace into which d2k is deployed and against which it translates Docker API calls. Only honoured when --d2k is set. Defaults to d2k.").Envar("KUBESOLO_D2K_NAMESPACE").Default("d2k").String()
)
