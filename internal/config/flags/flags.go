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
	PortainerEdgeID        = Application.Flag("portainer-edge-id", "Portainer Edge ID. Defaults to empty string.").Envar("KUBESOLO_PORTAINER_EDGE_ID").Default("").String()
	PortainerEdgeKey       = Application.Flag("portainer-edge-key", "Portainer Edge Key. Defaults to empty string.").Envar("KUBESOLO_PORTAINER_EDGE_KEY").Default("").String()
	PortainerEdgeAsync     = Application.Flag("portainer-edge-async", "Enable Portainer Edge Async Mode. Defaults to false.").Envar("KUBESOLO_PORTAINER_EDGE_ASYNC").Default("false").Bool()
	LoadBalancer           = Application.Flag("load-balancer", "Enable load balancer. With this enabled, kubesolo will update a newly deployed service with the load balancer type so that the EXTERNAL-IP is set to the node IP. Defaults to true.").Envar("KUBESOLO_LOAD_BALANCER").Default("true").Bool()
	LocalStorage           = Application.Flag("local-storage", "Enable local storage. Defaults to false.").Envar("KUBESOLO_LOCAL_STORAGE").Default("true").Bool()
	LocalStorageSharedPath = Application.Flag("local-storage-shared-path", "Path to the shared file system for the local storage. Defaults to empty string.").Envar("KUBESOLO_LOCAL_STORAGE_SHARED_PATH").Default("").String()
	Wasm                   = Application.Flag("wasm", "Enable WebAssembly runtime support via wasmtime").Envar("KUBESOLO_WASM").Default("false").Bool()
	Debug                  = Application.Flag("debug", "Enable debug logging. Defaults to false.").Envar("KUBESOLO_DEBUG").Default("false").Bool()
	PprofServer            = Application.Flag("pprof-server", "Enable pprof server. Defaults to false.").Envar("KUBESOLO_PPROF_SERVER").Default("false").Bool()
)
