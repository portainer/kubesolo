package flags

import "github.com/alecthomas/kingpin/v2"

// the full list of flags for the kubesolo application
// Path is the path to the directory containing the kubesolo configuration files
// PortainerEdgeID is the Edge ID for the Portainer Edge Agent
// PortainerEdgeKey is the Edge Key for the Portainer Edge Agent that can be used to register the Edge Agent with the Portainer Server
// PortainerEdgeAsync is the flag to enable Portainer Edge Async Mode
// LocalStorage is the flag to enable local storage
// Debug is the flag to enable debug logging
// PprofServer is the flag to enable the pprof server
var (
	Application        = kingpin.New("kubesolo", "Ultra-lightweight, OCI-compliant, single-node Kubernetes built for constrained environments such as IoT or IIoT devices running in embedded environments.")
	Path               = Application.Flag("path", "Path to the directory containing the kubesolo configuration files. Defaults to /var/lib/kubesolo.").Envar("KUBESOLO_PATH").Default("/var/lib/kubesolo").String()
	PortainerEdgeID    = Application.Flag("portainer-edge-id", "Portainer Edge ID. Defaults to empty string.").Envar("KUBESOLO_PORTAINER_EDGE_ID").Default("").String()
	PortainerEdgeKey   = Application.Flag("portainer-edge-key", "Portainer Edge Key. Defaults to empty string.").Envar("KUBESOLO_PORTAINER_EDGE_KEY").Default("").String()
	PortainerEdgeAsync = Application.Flag("portainer-edge-async", "Enable Portainer Edge Async Mode. Defaults to false.").Envar("KUBESOLO_PORTAINER_EDGE_ASYNC").Default("false").Bool()
	LocalStorage       = Application.Flag("local-storage", "Enable local storage. Defaults to true.").Envar("KUBESOLO_LOCAL_STORAGE").Default("true").Bool()
	Debug              = Application.Flag("debug", "Enable debug logging. Defaults to false.").Envar("KUBESOLO_DEBUG").Default("false").Bool()
	PprofServer        = Application.Flag("pprof-server", "Enable pprof server. Defaults to false.").Envar("KUBESOLO_PPROF_SERVER").Default("false").Bool()
)
