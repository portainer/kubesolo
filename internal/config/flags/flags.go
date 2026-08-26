package flags

import (
	"github.com/alecthomas/kingpin/v2"
	"github.com/portainer/kubesolo/types"
)

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
// MetricsServer is the flag to enable the kubesolo Prometheus metrics endpoint
// MetricsBindAddress is the host:port the metrics endpoint binds to
var (
	Application              = kingpin.New("kubesolo", "Ultra-lightweight, OCI-compliant, single-node Kubernetes built for constrained environments such as IoT or IIoT devices running in embedded environments.")
	Version                  = Application.Flag("version", "Show the version and exit.").Short('v').Bool()
	Path                     = tracked(Application.Flag("path", "Path to the directory containing the kubesolo configuration files. Defaults to /var/lib/kubesolo. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_PATH")).String()
	APIServerExtraSANs       = tracked(Application.Flag("apiserver-extra-sans", "A comma-separated list of additional Subject Alternative Names (SANs) to include in the API server's TLS certificate. These SANs can be IP addresses or DNS names (e.g., 10.0.0.4,kubesolo.local). Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_APISERVER_EXTRA_SANS")).String()
	NodeIP                   = tracked(Application.Flag("node-ip", "Override the auto-detected node IP. This IP is used for the API server advertise address, the generated kubeconfig, the kubelet node IP and the LoadBalancer EXTERNAL-IP. Useful on hosts with multiple NICs where auto-detection is ambiguous. When unset, kubesolo auto-detects and prefers a private (RFC 1918) address. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_NODE_IP")).String()
	MTU                      = tracked(Application.Flag("mtu", "Override the auto-detected network MTU used by the embedded CNI bridge (cni0) and pod veth interfaces. Useful on hosts whose primary interface has a reduced MTU (e.g. VPN/tunnel/PPPoE links). Defaults to 0, meaning auto-detect. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_MTU")).Int()
	PortainerEdgeID          = tracked(Application.Flag("portainer-edge-id", "Portainer Edge ID. Defaults to empty string. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_PORTAINER_EDGE_ID")).String()
	PortainerEdgeKey         = tracked(Application.Flag("portainer-edge-key", "Portainer Edge Key. Defaults to empty string. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_PORTAINER_EDGE_KEY")).String()
	PortainerEdgeAsync       = tracked(Application.Flag("portainer-edge-async", "Enable Portainer Edge Async Mode. Defaults to false. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_PORTAINER_EDGE_ASYNC")).Bool()
	PortainerEdgeImage       = tracked(Application.Flag("portainer-edge-image", "Full image reference deployed for the Portainer Edge Agent, including the tag. Any image other than the default is pulled from the registry rather than loaded from the embedded image. Defaults to docker.io/portainer/agent:lts. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_PORTAINER_EDGE_IMAGE")).String()
	LoadBalancer             = tracked(Application.Flag("load-balancer", "Enable load balancer. With this enabled, kubesolo sets the EXTERNAL-IP on services of type LoadBalancer, both when they are created and when an existing service is changed to that type. Defaults to true. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_LOAD_BALANCER")).Bool()
	LoadBalancerIP           = tracked(Application.Flag("load-balancer-ip", "Override the IP published as the LoadBalancer EXTERNAL-IP. Useful on hosts with multiple NICs where services should be advertised on a different address than the node IP. When unset, the node IP is used. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_LOAD_BALANCER_IP")).String()
	LocalStorage             = tracked(Application.Flag("local-storage", "Enable local storage. Defaults to false. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_LOCAL_STORAGE")).Bool()
	LocalStorageSharedPath   = tracked(Application.Flag("local-storage-shared-path", "Path to the shared file system for the local storage. Defaults to empty string. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_LOCAL_STORAGE_SHARED_PATH")).String()
	Debug                    = tracked(Application.Flag("debug", "Enable debug logging. Defaults to false. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_DEBUG")).Bool()
	PprofServer              = tracked(Application.Flag("pprof-server", "Enable pprof server. Defaults to false. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_PPROF_SERVER")).Bool()
	ContainerMode            = tracked(Application.Flag("container-mode", "Run in container mode with cgroupfs driver and relaxed eviction thresholds. Auto-detected when running inside a container. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_CONTAINER_MODE")).Bool()
	Full                     = Application.Flag("full", "Deprecated: has no effect. KubeSolo always uses upstream Kubernetes defaults. Retained for backwards compatibility and will be removed in a future release.").Envar("KUBESOLO_FULL").Default("false").Bool()
	DBWALRepair              = tracked(Application.Flag("db-wal-repair", "On startup, run an integrity check against the SQLite database and remove WAL artefacts (state.db-wal, state.db-shm) if corruption is detected. Recovers from unclean shutdowns caused by power loss. Defaults to false. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_DB_WAL_REPAIR")).Bool()
	DisableIPv6              = tracked(Application.Flag("disable-ipv6", "Disable IPv6 support. When set, CoreDNS will not serve ip6.arpa reverse zones and kubelet will register with an explicit IPv4 node address. Defaults to false. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_DISABLE_IPV6")).Bool()
	StartupTimeout           = tracked(Application.Flag("startup-timeout", "Maximum time in seconds to wait for each component to pass its health check during startup. Increase on slow storage such as SD cards. Defaults to 600. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_STARTUP_TIMEOUT")).Int()
	D2K                      = tracked(Application.Flag("d2k", "Enable d2k integration. When set, kubesolo deploys the Portainer d2k Docker-to-Kubernetes translator into the target namespace and exposes a Docker-compatible API endpoint over mTLS on port 2376. Defaults to false. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_D2K")).Bool()
	D2KNamespace             = tracked(Application.Flag("d2k-namespace", "Single namespace into which d2k is deployed and against which it translates Docker API calls. Only honoured when --d2k is set. Defaults to d2k. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_D2K_NAMESPACE")).String()
	ContainerRuntimeEndpoint = tracked(Application.Flag("container-runtime-endpoint", "CRI endpoint of a host-managed container runtime, for example unix:///run/containerd/containerd.sock or unix:///run/crio/crio.sock. When set, KubeSolo does not start its own embedded containerd and instead attaches to the given runtime; the host is then responsible for the runtime itself, the OCI runtime, the CNI plugin binaries and the sandbox image. When unset, KubeSolo runs its own embedded containerd. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_CONTAINER_RUNTIME_ENDPOINT")).String()
	MetricsServer            = tracked(Application.Flag("metrics-server", "Enable the kubesolo Prometheus metrics endpoint. Exposes a /metrics HTTP endpoint with control plane health gauges. Defaults to false. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_METRICS_SERVER")).Bool()
	MetricsBindAddress       = tracked(Application.Flag("metrics-bind-address", "Host:port the kubesolo metrics endpoint binds to. Defaults to 127.0.0.1:9105. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_METRICS_BIND_ADDRESS")).String()
	CPUManagerPolicy         = tracked(Application.Flag("cpu-manager-policy", "CPU manager policy used by the kubelet. Set to static to give Guaranteed-QoS pods that request whole CPUs exclusive access to those cores, which suits latency-sensitive workloads that must not be preempted by neighbours. Not supported in container mode. Defaults to none, meaning all pods share the CPU pool. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_CPU_MANAGER_POLICY")).String()
	CPUManagerPolicyOptions  = tracked(Application.Flag("cpu-manager-policy-options", "Comma-separated key=value options that fine-tune the static CPU manager policy, for example full-pcpus-only=true,strict-cpu-reservation=true. Only honoured when --cpu-manager-policy=static. Defaults to empty. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_CPU_MANAGER_POLICY_OPTIONS")).String()
	SystemReserved           = tracked(Application.Flag("system-reserved", "Comma-separated ResourceName=Quantity pairs withheld from node allocatable for the host, for example cpu=1,memory=500Mi. Supports cpu, memory, ephemeral-storage and pid. With the static CPU manager policy, cpu= satisfies its reservation requirement but lets the kubelet choose which cores are held back; use --reserved-cpus to name them instead. Defaults to empty. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_SYSTEM_RESERVED")).String()
	Config                   = Application.Flag("config", "Path to the KubeSolo configuration file.").Envar("KUBESOLO_CONFIG").Default(types.DefaultConfigFile).String()
	PrintConfig              = Application.Flag("print-config", "Print the effective configuration to stdout and exit, without starting anything. Useful for migrating an existing flag-based install: run it with the flags currently in the service unit and save the result as the config file.").Bool()
	ReservedCPUs             = tracked(Application.Flag("reserved-cpus", "Cpuset reserved for the host and kubesolo itself, for example 0 or 0-1. These CPUs are never given out as exclusive cores. Only honoured when --cpu-manager-policy=static, where it defaults to 0 because the static policy requires a non-empty reservation. Deprecated: set this in the KubeSolo config file instead.").Envar("KUBESOLO_RESERVED_CPUS")).String()
)

// setByUser records, per flag name, whether the value came from the command line.
//
// This exists because kingpin cannot answer the question any other way. A flag
// populated from its environment variable goes through the same path as one
// left at its default, so the flag's value is not evidence the user asked for
// it. Only this is. The loader resolves environment variables itself.
var setByUser = map[string]*bool{}

// tracked marks a flag so SetByUser can report on it. Every configurable flag is
// wrapped; --version and --full are not, being respectively not configuration
// and a no-op.
func tracked(f *kingpin.FlagClause) *kingpin.FlagClause {
	seen := new(bool)
	setByUser[f.Model().Name] = seen
	return f.IsSetByUser(seen)
}

// SetByUser reports which flags the user named on the command line. Only
// meaningful after Application.Parse.
func SetByUser() map[string]bool {
	out := make(map[string]bool, len(setByUser))
	for name, seen := range setByUser {
		out[name] = *seen
	}
	return out
}

// Values reports every flag's current value in string form, which is what the
// config loader applies. Only meaningful after Application.Parse.
func Values() map[string]string {
	out := map[string]string{}
	for _, m := range Application.Model().Flags {
		if m.Value == nil {
			continue
		}
		out[m.Name] = m.Value.String()
	}
	return out
}
