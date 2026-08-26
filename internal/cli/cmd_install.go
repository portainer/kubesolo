package cli

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/download"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/process"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
	kubesoloconfig "github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/internal/config/cpumanager"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func installCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install KubeSolo and configure the system service",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, cfg)
		},
	}
	addInstallFlags(cmd, cfg)
	return cmd
}

func addInstallFlags(cmd *cobra.Command, cfg *config.Config) {
	f := cmd.Flags()

	f.StringVar(&cfg.Version, "version",
		envOr("KUBESOLO_VERSION", config.DefaultVersion),
		"KubeSolo version to install (e.g. v1.1.8)")

	f.StringVar(&cfg.Path, "path",
		envOr("KUBESOLO_PATH", config.DefaultPath),
		"Base directory for KubeSolo data")

	f.StringVar(&cfg.APIServerExtraSANs, "apiserver-extra-sans",
		os.Getenv("KUBESOLO_APISERVER_EXTRA_SANS"),
		"Comma-separated extra Subject Alternative Names for the API server certificate")

	f.StringVar(&cfg.NodeIP, "node-ip",
		os.Getenv("KUBESOLO_NODE_IP"),
		"Override the auto-detected node IP (advertise address, kubeconfig, kubelet, LoadBalancer EXTERNAL-IP).\n"+
			"Useful on hosts with multiple NICs. Defaults to auto-detection (prefers a private address)")

	f.StringVar(&cfg.MTU, "mtu",
		os.Getenv("KUBESOLO_MTU"),
		"Override the auto-detected network MTU for the embedded CNI bridge (e.g. 1400 for VPN/tunnel interfaces).\n"+
			"In container mode this also sizes the outer Docker network the KubeSolo container runs on.\n"+
			"Defaults to auto-detection")

	f.StringVar(&cfg.PortainerEdgeID, "portainer-edge-id",
		os.Getenv("KUBESOLO_PORTAINER_EDGE_ID"),
		"Portainer edge agent ID")

	f.StringVar(&cfg.PortainerEdgeKey, "portainer-edge-key",
		os.Getenv("KUBESOLO_PORTAINER_EDGE_KEY"),
		"Portainer edge agent key")

	f.BoolVar(&cfg.PortainerEdgeAsync, "portainer-edge-async",
		envBool("KUBESOLO_PORTAINER_EDGE_ASYNC", false),
		"Enable async mode for the Portainer edge agent")

	f.StringVar(&cfg.PortainerEdgeImage, "portainer-edge-image",
		os.Getenv("KUBESOLO_PORTAINER_EDGE_IMAGE"),
		"Image deployed for the Portainer edge agent, including the tag\n"+
			"(default: docker.io/portainer/agent:lts). Any other image is pulled from the registry")

	f.BoolVar(&cfg.LocalStorage, "local-storage",
		envBool("KUBESOLO_LOCAL_STORAGE", false),
		"Enable the local-path storage provisioner")

	f.BoolVar(&cfg.Debug, "debug",
		envBool("KUBESOLO_DEBUG", false),
		"Enable debug logging")

	f.BoolVar(&cfg.PprofServer, "pprof-server",
		envBool("KUBESOLO_PPROF_SERVER", false),
		"Enable the pprof HTTP profiling server")

	f.StringVar(&cfg.RunMode, "run-mode",
		envOr("KUBESOLO_RUN_MODE", config.DefaultRunMode),
		"How to run KubeSolo: service (default), daemon, foreground, or container\n"+
			"(runs on Docker Engine; supported on macOS, Windows WSL2, or Linux)")

	f.StringVar(&cfg.Proxy, "proxy",
		os.Getenv("KUBESOLO_PROXY"),
		"HTTP/HTTPS proxy URL (injected into the service environment)")

	f.StringVar(&cfg.OfflineInstall, "offline-install",
		os.Getenv("KUBESOLO_OFFLINE_INSTALL"),
		"Path to a local tarball or binary to install instead of downloading")

	f.BoolVar(&cfg.InstallPrereqs, "install-prereqs",
		envBool("KUBESOLO_INSTALL_PREREQS", false),
		"Automatically install missing OS prerequisites (e.g. nftables on Alpine)")

	f.BoolVar(&cfg.D2K, "d2k",
		envBool("KUBESOLO_D2K", false),
		"Enable the d2k Docker-to-Kubernetes API translator on port 2376 (mTLS)")

	f.StringVar(&cfg.D2KNamespace, "d2k-namespace",
		envOr("KUBESOLO_D2K_NAMESPACE", "d2k"),
		"Namespace d2k is deployed into and translates Docker API calls against")

	f.StringVar(&cfg.CPUManagerPolicy, "cpu-manager-policy",
		envOr("KUBESOLO_CPU_MANAGER_POLICY", config.CPUManagerPolicyNone),
		"CPU manager policy: none (default) or static. The static policy gives Guaranteed-QoS pods\n"+
			"that request whole CPUs exclusive cores, for latency-sensitive workloads.\n"+
			"Not supported in container run mode")

	f.StringVar(&cfg.CPUManagerPolicyOptions, "cpu-manager-policy-options",
		os.Getenv("KUBESOLO_CPU_MANAGER_POLICY_OPTIONS"),
		"Comma-separated key=value options for the static CPU manager policy\n"+
			"(e.g. full-pcpus-only=true,strict-cpu-reservation=true)")

	f.StringVar(&cfg.SystemReserved, "system-reserved",
		os.Getenv("KUBESOLO_SYSTEM_RESERVED"),
		"Comma-separated ResourceName=Quantity pairs withheld from node allocatable for the host\n"+
			"(e.g. cpu=1,memory=500Mi). Supports cpu, memory, ephemeral-storage and pid.\n"+
			"With the static policy, cpu= lets the kubelet choose which cores are held back")

	f.StringVar(&cfg.ReservedCPUs, "reserved-cpus",
		os.Getenv("KUBESOLO_RESERVED_CPUS"),
		"Cpuset reserved for the host and KubeSolo itself, never given out as an exclusive core\n"+
			"(e.g. 0 or 0-1). Defaults to 0 when the static policy is used")

	f.StringVar(&cfg.ContainerImage, "image",
		os.Getenv("KUBESOLO_IMAGE"),
		"Container image to use in container mode (default: portainer/kubesolo:<version>).\n"+
			"Accepts a full reference including tag, e.g. portainerci/kubesolo:pr-42-linux-arm64")

	f.StringVar(&cfg.ContainerPorts, "container-ports",
		os.Getenv("KUBESOLO_CONTAINER_PORTS"),
		"Container-mode only: comma-separated host ports to publish for workloads\n"+
			"(e.g. --container-ports=9001,8080:80,9000-9100,53/udp). Bare ports and\n"+
			"ranges map host==container; explicit host:container forms are honored.")

	f.StringVar(&cfg.Name, "name",
		envOr("KUBESOLO_NAME", config.AppName),
		"Name for this KubeSolo instance — used as the container name and kubeconfig context")
}

func runInstall(cmd *cobra.Command, cfg *config.Config) error {
	p := ui.New()
	p.Header("install")

	// On macOS, KubeSolo must run as a container — the binary is Linux-only.
	if runtime.GOOS == "darwin" {
		if cmd.Flags().Changed("run-mode") && cfg.RunMode != config.RunModeContainer {
			return fmt.Errorf("on macOS, only --run-mode=container is supported (KubeSolo is Linux-only and runs inside a container)")
		}
		cfg.RunMode = config.RunModeContainer
	}

	containerMode := cfg.RunMode == config.RunModeContainer

	// ── Feature/version compatibility ──────────────────────────────────────────
	// d2k flags only exist in kubesolo >= MinD2KVersion. Passing --d2k to an older
	// binary makes it exit 1 on every start (systemd then crash-loops it). A custom
	// --image overrides the version entirely, so skip the check in that case.
	if cfg.D2K && (!containerMode || cfg.ContainerImage == "") {
		if cmp, ok := compareVersions(cfg.Version, config.MinD2KVersion); ok && cmp < 0 {
			return p.Fail("version check", fmt.Errorf(
				"--d2k requires kubesolo %s or newer; %s has no d2k support — re-run with --version=%s (or later)",
				config.MinD2KVersion, cfg.Version, config.MinD2KVersion))
		}
	}

	// ── CPU pinning ─────────────────────────────────────────────────────────────
	// Validated here with the same parser the kubesolo binary uses, so a bad flag
	// fails before a service unit is written rather than crash-looping afterwards.
	if containerMode && cfg.CPUManagerPolicy != "" && cfg.CPUManagerPolicy != config.CPUManagerPolicyNone {
		return p.Fail("cpu pinning", fmt.Errorf(
			"--cpu-manager-policy=%s is not supported in container run mode: exclusive cores are bounded by the container's own cpuset, which KubeSolo does not control",
			cfg.CPUManagerPolicy))
	}
	if _, _, err := cpumanager.Parse(cfg.CPUManagerPolicy, cfg.CPUManagerPolicyOptions, cfg.ReservedCPUs, cfg.SystemReserved, runtime.NumCPU()); err != nil {
		return p.Fail("cpu pinning", err)
	}

	// ── Container port mappings ─────────────────────────────────────────────────
	// Only meaningful in container mode — a host install runs on the host network,
	// so workloads bind host ports directly. Validate early so a typo fails before
	// any system changes are made.
	if cfg.ContainerPorts != "" {
		if !containerMode {
			p.Warn("--container-ports is ignored outside container mode (host installs use the host network directly)")
			cfg.ContainerPorts = ""
		} else if _, _, err := service.ParseContainerPorts(cfg.ContainerPorts); err != nil {
			return p.Fail("container ports", err)
		}
	}

	// ── Detect system ─────────────────────────────────────────────────────────
	p.Step("Detecting system")
	info, err := detect.Detect()
	if err != nil {
		return p.Fail("system detection", err)
	}
	if !containerMode {
		// On Linux, stop any existing KubeSolo before the step closes — its log
		// output appears as detail lines under the step.
		process.StopAll(initControlBinary(info.InitSystem))
		process.CleanupFileConflicts(cfg.Path)
		p.OK("System detected", fmt.Sprintf("%s · %s", info.Arch, info.LibC))
		p.Info(fmt.Sprintf("Init system:  %s", info.InitSystem))
	} else {
		p.OK("System detected", fmt.Sprintf("%s/%s", info.OS, info.Arch))
		p.Info("Run mode:  container")
	}

	// ── Pre-flight ────────────────────────────────────────────────────────────
	p.Step("Pre-flight checks")
	var checks []preflight.Check
	if containerMode {
		checks = preflight.ContainerSuite()
	} else {
		checks = preflight.Suite(cfg.InstallPrereqs, cfg.PprofServer)
	}
	if err := preflight.RunSuite(checks); err != nil {
		return p.Fail("pre-flight checks", err)
	}
	p.OK(fmt.Sprintf("Pre-flight checks passed (%d/%d)", len(checks), len(checks)), "")

	// ── Download / install binary (host mode only) ────────────────────────────
	if !containerMode {
		p.Step(fmt.Sprintf("Installing KubeSolo %s", cfg.Version))
		if err := download.Install(cfg.OfflineInstall, info.ArchiveName(cfg.Version), cfg.Version); err != nil {
			return p.Fail("binary installation", err)
		}
		restoreSELinux(config.DefaultInstallPath)
		p.OK(fmt.Sprintf("KubeSolo %s installed", cfg.Version), config.DefaultInstallPath)
	}

	// ── Configure service / start container ──────────────────────────────────
	if containerMode {
		p.Step(fmt.Sprintf("Starting KubeSolo %s container", cfg.Version))
	} else {
		p.Step(fmt.Sprintf("Configuring %s service", info.InitSystem))
	}
	// ── Configuration file ────────────────────────────────────────────────────
	// Written before the service is defined, so its command line can be the
	// single --config flag. Container mode is excluded — see writeConfigFile.
	if err := writeConfigFile(p, cfg, containerMode); err != nil {
		return p.Fail("configuration", err)
	}

	mgr, err := service.New(info, cfg.RunMode, cfg.Name)
	if err != nil {
		return p.Fail("service setup", err)
	}
	if err := mgr.Install(cfg, cfg.CmdArgs()); err != nil {
		return p.Fail("service setup", err)
	}

	// In container mode, discover the random host port the engine assigned, wait
	// for the kubeconfig, and keep progress under the "Starting container" step.
	var containerKubeconfig []byte
	var apiAddr string
	if containerMode {
		port, err := service.GetContainerAPIPort(cfg.Name)
		if err != nil {
			p.Warn("could not determine API server port — falling back to 6443")
			apiAddr = "127.0.0.1:6443"
		} else {
			apiAddr = fmt.Sprintf("127.0.0.1:%d", port)
		}
		containerKubeconfig, _ = kubeconfig.WaitForContainerKubeconfig(service.ContainerNameFor(cfg.Name), "")
		p.OK(fmt.Sprintf("KubeSolo %s container running", cfg.Version), cfg.Name)
	} else {
		p.OK(fmt.Sprintf("%s service configured", info.InitSystem), "")
	}

	// ── Kubeconfig ────────────────────────────────────────────────────────────
	if cfg.RunMode != config.RunModeForeground {
		p.Step("Merging kubeconfig")
		if containerMode {
			kubeconfig.MergeContainerKubeconfig(containerKubeconfig, cfg.Name, "https://"+apiAddr)
			p.OK("Kubeconfig merged", "~/.kube/config")
		} else if err := kubeconfig.MergeAfterStartup(cfg.Path); err != nil {
			// Don't claim success — the cluster is still coming up. The kubeconfig
			// is already on disk; the user can merge it once the API server is ready.
			p.Warn("kubeconfig not merged yet — " + err.Error())
			p.Info("Once ready, run:  kubesoloctl kubeconfig fetch")
		} else {
			p.OK("Kubeconfig merged", "~/.kube/config")
		}
	}

	// ── Wait for API server ───────────────────────────────────────────────────
	if containerMode {
		p.Step("Waiting for API server")
		if err := kubeconfig.WaitForAPIServer(apiAddr, 120*time.Second); err != nil {
			p.Warn("API server not yet ready — it may still be initializing")
		} else {
			p.OK("API server ready", "https://"+apiAddr)
		}
	}

	p.Done(fmt.Sprintf("KubeSolo %s is running", cfg.Version))

	// ── Post-install footer ───────────────────────────────────────────────────
	if cfg.RunMode != config.RunModeForeground {
		p.Cmd("kubectl get nodes --watch")
		p.Cmd("kubectl get pods -A")
		p.Blank()

		app := config.AppName
		if containerMode {
			p.Label("Refresh", "kubesoloctl kubeconfig fetch")
		} else {
			switch info.InitSystem {
			case detect.InitSystemd:
				p.Label("Manage", fmt.Sprintf("systemctl status %s", app))
				p.Label("Logs", fmt.Sprintf("journalctl -u %s -f", app))
			case detect.InitOpenRC:
				p.Label("Manage", fmt.Sprintf("rc-service %s status", app))
				p.Label("Logs", "tail -f /var/log/messages")
			case detect.InitSysV:
				p.Label("Manage", fmt.Sprintf("service %s status", app))
				p.Label("Logs", "tail -f /var/log/syslog")
			default:
				p.Label("Logs", fmt.Sprintf("tail -f %s", config.LogFile))
			}
		}

		if cfg.D2K {
			p.Label("D2K", "kubesoloctl d2k fetch")
		}
	}

	return nil
}

// writeConfigFile renders the installer's settings into the KubeSolo
// configuration document and saves it, then points cfg at it so the service
// command line collapses to a single --config flag.
//
// Two cases keep their flags instead.
//
// Older binaries predate --config entirely; passing it would abort the service
// on every start, so kubesoloctl can still install them the old way.
//
// Container run mode stores KubeSolo's state in a Docker named volume rather
// than on the host, so there is no host directory to write the file into and
// bind-mount back. /etc is also not shared into Docker Desktop on macOS by
// default, where container mode is the only supported mode. Container mode is a
// developer and CI convenience, so it keeps the flag-based command line.
func writeConfigFile(p *ui.Printer, cfg *config.Config, containerMode bool) error {
	if containerMode {
		return nil
	}
	if cmp, ok := compareVersions(cfg.Version, config.MinConfigFileVersion); ok && cmp < 0 {
		log.Info().Msgf("kubesolo %s predates the configuration file; installing with flags instead", cfg.Version)
		return nil
	}

	cfg.ConfigFile = types.DefaultConfigFile

	doc, warnings, err := cfg.ToKubeSoloConfig()
	for _, w := range warnings {
		p.Warn(w.String())
	}
	if err != nil {
		return err
	}

	if err := kubesoloconfig.Write(cfg.ConfigFile, doc); err != nil {
		return err
	}

	p.OK("Configuration written", cfg.ConfigFile)
	return nil
}
