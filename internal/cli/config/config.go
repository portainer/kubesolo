package config

const (
	DefaultVersion     = "v1.1.8"
	DefaultPath        = "/var/lib/kubesolo"
	DefaultInstallPath = "/usr/local/bin/kubesolo"
	DefaultRunMode     = RunModeService
	AppName            = "kubesolo"
	PIDFile            = "/var/run/kubesolo.pid"
	LogFile            = "/var/log/kubesolo.log"

	// MinD2KVersion is the first KubeSolo release whose binary understands the
	// --d2k / --d2k-namespace flags (d2k integration landed after v1.1.5).
	// The installer refuses to pass --d2k to an older binary, which would crash-loop.
	MinD2KVersion = "v1.1.8"

	RunModeService    = "service"
	RunModeDaemon     = "daemon"
	RunModeForeground = "foreground"
	RunModeContainer  = "container" // KubeSolo runs inside a container (macOS, Windows WSL2, or Linux with a container engine)

	// DefaultContainerImage is the container image used in container run mode.
	DefaultContainerImage = "portainer/kubesolo"
)

// Config holds all configuration for the installer, sourced from CLI flags
// and environment variables. CLI flags take precedence over environment variables.
type Config struct {
	// Name identifies this KubeSolo instance. Used as the container name
	// and kubeconfig context name. Defaults to AppName ("kubesolo").
	Name string

	// Version of KubeSolo to install (e.g. "v1.1.8")
	Version string

	// Path is the base directory KubeSolo stores its data in
	Path string

	// APIServerExtraSANs is an optional comma-separated list of extra SANs for the API server certificate
	APIServerExtraSANs string

	// NodeIP optionally overrides the auto-detected node IP (useful on multi-NIC hosts)
	NodeIP string

	// MTU optionally overrides the auto-detected network MTU used by the embedded
	// CNI bridge (cni0) and pod veth interfaces, and — in container run mode —
	// the outer Docker network the KubeSolo container itself runs on (the two
	// must match; see internal/cli/service/container.go). Empty means auto-detect.
	MTU string

	// PortainerEdgeID is the Portainer edge agent ID
	PortainerEdgeID string

	// PortainerEdgeKey is the Portainer edge agent key
	PortainerEdgeKey string

	// PortainerEdgeAsync enables async mode for the Portainer edge agent
	PortainerEdgeAsync bool

	// PortainerEdgeImage overrides the image deployed for the Portainer edge agent.
	// Empty means the flag is not passed and the KubeSolo binary uses its default.
	PortainerEdgeImage string

	// LocalStorage enables the local-path storage provisioner
	LocalStorage bool

	// Debug enables verbose debug logging in the installed KubeSolo process
	Debug bool

	// PprofServer enables the pprof HTTP server in the installed KubeSolo process
	PprofServer bool

	// RunMode controls how KubeSolo is started: "service" (default), "daemon", or "foreground"
	RunMode string

	// Proxy is an optional HTTP/HTTPS proxy URL injected into the service environment
	Proxy string

	// OfflineInstall is a path to a local binary or tarball to install instead of downloading
	OfflineInstall string

	// InstallPrereqs causes the installer to automatically install missing OS-level
	// prerequisites (e.g. nftables on Alpine Linux) instead of hard-failing
	InstallPrereqs bool

	// D2K enables the d2k Docker-to-Kubernetes API translator
	D2K bool

	// D2KNamespace is the namespace d2k is deployed into and translates against
	D2KNamespace string

	// ContainerImage is the container image reference used in container run mode.
	// If empty, defaults to DefaultContainerImage:Version.
	// Specify a full reference (e.g. "myrepo/kubesolo:custom") to override entirely.
	ContainerImage string

	// ContainerPorts is a comma-separated list of host↔container port mappings to
	// publish in container run mode (e.g. "9001,8080:80,9000-9100,53/udp"). Bare
	// ports and ranges map host==container. It is a container-runtime concern and
	// is deliberately NOT passed to the kubesolo binary via CmdArgs.
	ContainerPorts string
}

// CmdArgs builds the argument list that will be passed to the kubesolo binary
// when constructing service files or launching in daemon/foreground mode.
func (c *Config) CmdArgs() []string {
	args := []string{"--path=" + c.Path}

	// Container mode targets CI/developer environments where memory is not
	// constrained, so always run with upstream Kubernetes defaults (--full)
	// instead of the edge memory-saving overrides.
	if c.RunMode == RunModeContainer {
		args = append(args, "--full")
	}

	if c.APIServerExtraSANs != "" {
		args = append(args, "--apiserver-extra-sans="+c.APIServerExtraSANs)
	}

	if c.NodeIP != "" {
		args = append(args, "--node-ip="+c.NodeIP)
	}

	if c.MTU != "" {
		args = append(args, "--mtu="+c.MTU)
	}

	if c.PortainerEdgeID != "" {
		args = append(args, "--portainer-edge-id="+c.PortainerEdgeID)
	}

	if c.PortainerEdgeKey != "" {
		args = append(args, "--portainer-edge-key="+c.PortainerEdgeKey)
	}

	if c.PortainerEdgeAsync {
		args = append(args, "--portainer-edge-async")
	}

	if c.PortainerEdgeImage != "" {
		args = append(args, "--portainer-edge-image="+c.PortainerEdgeImage)
	}

	if c.LocalStorage {
		args = append(args, "--local-storage")
	}

	if c.Debug {
		args = append(args, "--debug")
	}

	if c.PprofServer {
		args = append(args, "--pprof-server")
	}

	if c.D2K {
		args = append(args, "--d2k")
	}

	if c.D2K && c.D2KNamespace != "" {
		args = append(args, "--d2k-namespace="+c.D2KNamespace)
	}

	// Proxy is intentionally omitted here: it is injected as HTTP_PROXY /
	// HTTPS_PROXY / NO_PROXY environment variables into the service unit or
	// daemon environment (see service templates and daemon.go). Go's net/http
	// transport reads those variables automatically, so no --proxy flag is
	// needed on the kubesolo command line.
	return args
}
