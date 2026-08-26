package config

import (
	"runtime"
	"strings"

	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/types"
)

const (
	DefaultVersion     = "v1.1.8"
	DefaultPath        = "/var/lib/kubesolo"
	DefaultInstallPath = "/usr/local/bin/kubesolo"
	DefaultRunMode     = RunModeService
	AppName            = "kubesolo"
	PIDFile            = "/var/run/kubesolo.pid"
	LogFile            = "/var/log/kubesolo.log"

	// CNIConfigFile is the CNI configuration KubeSolo drops into the standard CNI
	// directory, as a symlink when it runs its own containerd and as a regular file
	// when it attaches to a host-managed runtime. Mirrors
	// types.DefaultStandardCNIConfDir + types.DefaultCNIConfigName in the kubesolo
	// binary, the same way DefaultPath mirrors its --path default.
	CNIConfigFile = "/etc/cni/net.d/10-bridge.conflist"

	// CPUManagerPolicyNone is the default kubelet CPU manager policy, in which every
	// pod shares all CPUs. Mirrors types.CPUManagerPolicyNone in the kubesolo binary.
	CPUManagerPolicyNone = "none"

	// MinConfigFileVersion is the first KubeSolo release whose binary understands
	// --config. Older binaries are installed with the full flag list instead:
	// passing them --config would abort the service on every start.
	MinConfigFileVersion = "v1.3.0"

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

	// CPUManagerPolicy selects the kubelet CPU manager policy ("none" or "static").
	// The static policy gives Guaranteed-QoS pods requesting whole CPUs exclusive
	// cores, and is unsupported in container run mode.
	CPUManagerPolicy string

	// CPUManagerPolicyOptions is a comma-separated list of key=value options that
	// fine-tune the static CPU manager policy
	CPUManagerPolicyOptions string

	// ReservedCPUs is the cpuset held back for the host and KubeSolo itself, and
	// never handed out as an exclusive core (e.g. "0" or "0-1")
	ReservedCPUs string

	// SystemReserved is a comma-separated ResourceName=Quantity list withheld from
	// node allocatable for the host (e.g. "cpu=1,memory=500Mi")
	SystemReserved string

	// ContainerImage is the container image reference used in container run mode.
	// If empty, defaults to DefaultContainerImage:Version.
	// Specify a full reference (e.g. "myrepo/kubesolo:custom") to override entirely.
	ContainerImage string

	// ContainerPorts is a comma-separated list of host↔container port mappings to
	// publish in container run mode (e.g. "9001,8080:80,9000-9100,53/udp"). Bare
	// ports and ranges map host==container. It is a container-runtime concern and
	// is deliberately NOT passed to the kubesolo binary via CmdArgs.
	ContainerPorts string

	// ConfigFile is where the KubeSolo configuration document is written, and
	// the only flag passed to the binary once it is. Empty means the target
	// binary predates the configuration file and must be given flags instead.
	ConfigFile string
}

// CmdArgs builds the argument list passed to the kubesolo binary when
// constructing service files or launching in daemon/foreground mode.
//
// When the target binary understands --config, that is the whole command line:
// every setting lives in the configuration file instead. Older binaries predate
// the file and still get the full flag list, so kubesoloctl can install them.
func (c *Config) CmdArgs() []string {
	if c.ConfigFile != "" {
		return []string{"--config=" + c.ConfigFile}
	}
	return c.kubesoloFlags()
}

// kubesoloFlags renders the KubeSolo settings as flags.
//
// It remains the single description of how an installer setting maps onto a
// KubeSolo one: ToKubeSoloConfig resolves these same flags through the loader
// the binary itself uses, so the two cannot drift.
func (c *Config) kubesoloFlags() []string {
	args := []string{"--path=" + c.Path}

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

	if c.CPUManagerPolicy != "" && c.CPUManagerPolicy != CPUManagerPolicyNone {
		args = append(args, "--cpu-manager-policy="+c.CPUManagerPolicy)
	}

	if c.CPUManagerPolicyOptions != "" {
		args = append(args, "--cpu-manager-policy-options="+c.CPUManagerPolicyOptions)
	}

	if c.ReservedCPUs != "" {
		args = append(args, "--reserved-cpus="+c.ReservedCPUs)
	}

	if c.SystemReserved != "" {
		args = append(args, "--system-reserved="+c.SystemReserved)
	}

	// Proxy is intentionally omitted here: it is injected as HTTP_PROXY /
	// HTTPS_PROXY / NO_PROXY environment variables into the service unit or
	// daemon environment (see service templates and daemon.go). Go's net/http
	// transport reads those variables automatically, so no --proxy flag is
	// needed on the kubesolo command line.
	return args
}

// ToKubeSoloConfig resolves the installer's settings into the KubeSolo
// configuration document that will be written to ConfigFile.
//
// The mapping is not written out a second time here. kubesoloFlags renders the
// settings as flags, and those flags are resolved through the same loader the
// kubesolo binary uses, against the same field registry. A setting can therefore
// not mean one thing to the installer and another to KubeSolo.
//
// The environment is deliberately excluded: kubesoloctl's own environment is not
// the installed service's, and folding it in would write values into the file
// that the operator never asked for.
func (c *Config) ToKubeSoloConfig() (*types.Config, []kubesoloconfig.Warning, error) {
	values, setByUser := splitFlagArgs(c.kubesoloFlags())

	cfg, warnings, err := kubesoloconfig.LoadWithoutEnv("", kubesoloconfig.FlagValues{
		Values:    values,
		SetByUser: setByUser,
	})
	if err != nil {
		return nil, warnings, err
	}

	validationWarnings, err := kubesoloconfig.Validate(cfg, kubesoloconfig.Host{
		NumCPU: runtime.NumCPU(),
		GOARCH: runtime.GOARCH,

		// kubesoloctl cannot know whether the installed KubeSolo will run inside
		// a container, so it reports the answer it does know. Container run mode
		// is rejected separately in runInstall, before this is reached.
		ContainerMode: c.RunMode == RunModeContainer,
	})
	return cfg, append(warnings, validationWarnings...), err
}

// splitFlagArgs turns "--name=value" and bare "--name" into the two maps the
// loader consumes. A bare flag is boolean and means true.
func splitFlagArgs(args []string) (values map[string]string, setByUser map[string]bool) {
	values, setByUser = map[string]string{}, map[string]bool{}

	for _, arg := range args {
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !hasValue {
			value = "true"
		}
		values[name] = value
		setByUser[name] = true
	}

	return values, setByUser
}
