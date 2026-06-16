package cli

import (
	"fmt"
	"os"
	"path/filepath"
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
		"KubeSolo version to install (e.g. v1.1.5)")

	f.StringVar(&cfg.Path, "path",
		envOr("KUBESOLO_PATH", config.DefaultPath),
		"Base directory for KubeSolo data")

	f.StringVar(&cfg.APIServerExtraSANs, "apiserver-extra-sans",
		os.Getenv("KUBESOLO_APISERVER_EXTRA_SANS"),
		"Comma-separated extra Subject Alternative Names for the API server certificate")

	f.StringVar(&cfg.PortainerEdgeID, "portainer-edge-id",
		os.Getenv("KUBESOLO_PORTAINER_EDGE_ID"),
		"Portainer edge agent ID")

	f.StringVar(&cfg.PortainerEdgeKey, "portainer-edge-key",
		os.Getenv("KUBESOLO_PORTAINER_EDGE_KEY"),
		"Portainer edge agent key")

	f.BoolVar(&cfg.PortainerEdgeAsync, "portainer-edge-async",
		envBool("KUBESOLO_PORTAINER_EDGE_ASYNC", false),
		"Enable async mode for the Portainer edge agent")

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
		"How to run KubeSolo: service (default), daemon, foreground, or container (macOS)")

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

	f.StringVar(&cfg.ContainerImage, "image",
		os.Getenv("KUBESOLO_IMAGE"),
		"Docker image to use in container mode (default: portainer/kubesolo:<version>).\n"+
			"Accepts a full reference including tag, e.g. portainerci/kubesolo:pr-42-linux-arm64")

	f.StringVar(&cfg.Name, "name",
		envOr("KUBESOLO_NAME", config.AppName),
		"Name for this KubeSolo instance — used as the Docker container name and kubeconfig context")
}

func runInstall(cmd *cobra.Command, cfg *config.Config) error {
	p := ui.New()
	p.Header("install")

	// On macOS, KubeSolo must run as a Docker container — the binary is Linux-only.
	// Auto-add 127.0.0.1 to SANs so kubectl works via Docker's published port 6443.
	if runtime.GOOS == "darwin" {
		if cmd.Flags().Changed("run-mode") && cfg.RunMode != config.RunModeContainer {
			return fmt.Errorf("on macOS, only --run-mode=container is supported (KubeSolo is Linux-only and runs inside Docker)")
		}
		cfg.RunMode = config.RunModeContainer
	}

	containerMode := cfg.RunMode == config.RunModeContainer

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
		p.OK("System detected", fmt.Sprintf("%s · %s · %s", info.Arch, info.LibC, info.InitSystem))
	} else {
		p.OK("System detected", fmt.Sprintf("%s/%s · container mode", info.OS, info.Arch))
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

	// ── Download / install binary (Linux only) ────────────────────────────────
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
	mgr, err := service.New(info, cfg.RunMode, cfg.Name)
	if err != nil {
		return p.Fail("service setup", err)
	}
	if err := mgr.Install(cfg, cfg.CmdArgs()); err != nil {
		return p.Fail("service setup", err)
	}

	// In container mode, discover the random host port Docker assigned, wait for
	// the kubeconfig, and keep progress under the "Starting container" step.
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
		} else {
			kubeconfig.MergeAfterStartup(cfg.Path)
		}
		p.OK("Kubeconfig merged", "~/.kube/config")
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

	// ── Post-install hints ────────────────────────────────────────────────────
	if cfg.RunMode != config.RunModeForeground {
		p.Section("Next steps")

		if containerMode {
			cname := service.ContainerNameFor(cfg.Name)
			p.Info(fmt.Sprintf("Manage:  docker ps --filter name=%s", cname))
			p.Info(fmt.Sprintf("Logs:    docker logs -f %s", cname))
			p.Info(fmt.Sprintf("Stop:    docker stop %s", cname))
			p.Info("")
			p.Info("Tip: kubesoloctl kubeconfig fetch  (to refresh kubeconfig)")
		} else {
			app := config.AppName
			switch info.InitSystem {
			case detect.InitSystemd:
				p.Info(fmt.Sprintf("Manage:  systemctl status %s", app))
				p.Info(fmt.Sprintf("Logs:    journalctl -u %s -f", app))
			case detect.InitOpenRC:
				p.Info(fmt.Sprintf("Manage:  rc-service %s status", app))
				p.Info("Logs:    tail -f /var/log/messages")
			case detect.InitSysV:
				p.Info(fmt.Sprintf("Manage:  service %s status", app))
				p.Info("Logs:    tail -f /var/log/syslog")
			default:
				p.Info(fmt.Sprintf("Logs:    tail -f %s", config.LogFile))
			}

			p.Info("")
			p.Info("Tip: if running KubeSolo in a Docker container, fetch the kubeconfig with:")
			p.Info("     kubesoloctl kubeconfig fetch")
		}

		// d2k context setup hint
		if cfg.D2K {
			pki := filepath.Join(cfg.Path, "pki")
			kcPath := filepath.Join(pki, "admin", "admin.kubeconfig")
			p.Hint("d2k: Docker-compatible API on port 2376 (mTLS). Once started:",
				"CERT_DIR=~/.config/d2k && mkdir -p $CERT_DIR",
				fmt.Sprintf("cp %s/ca/ca.crt $CERT_DIR/ca.crt", pki),
				fmt.Sprintf("cp %s/d2k/client.crt $CERT_DIR/client.crt", pki),
				fmt.Sprintf("cp %s/d2k/client.key $CERT_DIR/client.key", pki),
				fmt.Sprintf("NODE=$(KUBECONFIG=%s kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type==\"InternalIP\")].address}')", kcPath),
				`docker context create d2k --docker "host=tcp://$NODE:2376,ca=$CERT_DIR/ca.crt,cert=$CERT_DIR/client.crt,key=$CERT_DIR/client.key"`,
				"docker context use d2k",
			)
		}
	}

	return nil
}
