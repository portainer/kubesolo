package config

const (
	DefaultVersion     = "v1.1.5"
	DefaultPath        = "/var/lib/kubesolo"
	DefaultInstallPath = "/usr/local/bin/kubesolo"
	DefaultRunMode     = RunModeService
	AppName            = "kubesolo"
	PIDFile            = "/var/run/kubesolo.pid"
	LogFile            = "/var/log/kubesolo.log"

	RunModeService    = "service"
	RunModeDaemon     = "daemon"
	RunModeForeground = "foreground"
)

// Config holds all configuration for the installer, sourced from CLI flags
// and environment variables. CLI flags take precedence over environment variables.
type Config struct {
	// Version of KubeSolo to install (e.g. "v1.1.5")
	Version string

	// Path is the base directory KubeSolo stores its data in
	Path string

	// APIServerExtraSANs is an optional comma-separated list of extra SANs for the API server certificate
	APIServerExtraSANs string

	// PortainerEdgeID is the Portainer edge agent ID
	PortainerEdgeID string

	// PortainerEdgeKey is the Portainer edge agent key
	PortainerEdgeKey string

	// PortainerEdgeAsync enables async mode for the Portainer edge agent
	PortainerEdgeAsync bool

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
}

// CmdArgs builds the argument list that will be passed to the kubesolo binary
// when constructing service files or launching in daemon/foreground mode.
func (c *Config) CmdArgs() []string {
	args := []string{"--path=" + c.Path}

	if c.APIServerExtraSANs != "" {
		args = append(args, "--apiserver-extra-sans="+c.APIServerExtraSANs)
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

	if c.LocalStorage {
		args = append(args, "--local-storage")
	}

	if c.Debug {
		args = append(args, "--debug")
	}

	if c.PprofServer {
		args = append(args, "--pprof-server")
	}

	// Proxy is intentionally omitted here: it is injected as HTTP_PROXY /
	// HTTPS_PROXY / NO_PROXY environment variables into the service unit or
	// daemon environment (see service templates and daemon.go). Go's net/http
	// transport reads those variables automatically, so no --proxy flag is
	// needed on the kubesolo command line.
	return args
}
