package flags

import "github.com/alecthomas/kingpin/v2"

var (
	Application      = kingpin.New("kubesolo", "Ultra-lightweight, OCI-compliant, single-node Kubernetes built for constrained environments such as IoT or IIoT devices running in embedded environments.")
	Path             = Application.Flag("path", "Path to the directory containing the kubesolo configuration files. Defaults to /var/lib/kubesolo.").Default("/var/lib/kubesolo").String()
	PortainerEdgeID  = Application.Flag("portainer-edge-id", "Portainer Edge ID. Defaults to empty string.").Default("").String()
	PortainerEdgeKey = Application.Flag("portainer-edge-key", "Portainer Edge Key. Defaults to empty string.").Default("").String()
	Debug            = Application.Flag("debug", "Enable debug logging. Defaults to false.").Default("false").Bool()
	PprofServer      = Application.Flag("pprof-server", "Enable pprof server. Defaults to false.").Default("false").Bool()
)
