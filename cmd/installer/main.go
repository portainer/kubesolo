// cmd/installer is a fully compiled, cross-platform replacement for install.sh,
// kubesolo-service.sh, and uninstall.sh.
//
// It is built with CGO_ENABLED=0 so a single binary per architecture covers
// both glibc and musl systems — eliminating the libc split the bash script
// has to handle at runtime.
//
// Usage (mirrors the existing bash installer):
//
//	sudo ./installer [flags]
//	sudo ./installer install [flags]
//	sudo ./installer uninstall
//	sudo ./installer service start|stop|restart|status|logs
//	sudo ./installer download [--version=v1.x.x] [--path=./]
//	./installer check
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/portainer/kubesolo/internal/installer/config"
	"github.com/portainer/kubesolo/internal/installer/detect"
	"github.com/portainer/kubesolo/internal/installer/download"
	"github.com/portainer/kubesolo/internal/installer/kubeconfig"
	"github.com/portainer/kubesolo/internal/installer/preflight"
	"github.com/portainer/kubesolo/internal/installer/process"
	"github.com/portainer/kubesolo/internal/installer/service"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	Version   = "dev"
	BuildDate = "unknown"
	Commit    = "unknown"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// rootCmd builds the top-level cobra command tree.
func rootCmd() *cobra.Command {
	var cfg config.Config

	// ── root (implicit install) ───────────────────────────────────────────────
	root := &cobra.Command{
		Use:   "installer",
		Short: "KubeSolo installer — single-node Kubernetes, anywhere",
		Long: `KubeSolo installer sets up KubeSolo and its system service on the current host.

Running without a sub-command is equivalent to running 'installer install'.

Examples:
  # Standard install (auto-detects init system):
  sudo installer

  # Specify version and Portainer edge credentials:
  sudo installer --version=v1.1.2 --portainer-edge-id=ID --portainer-edge-key=KEY

  # Air-gap install from a local archive:
  sudo installer --offline-install=/tmp/kubesolo-v1.1.2-linux-amd64.tar.gz

  # Check pre-flight conditions without installing:
  installer check`,
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			configureLogging(cfg.Debug)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(&cfg)
		},
	}

	addInstallFlags(root, &cfg)

	root.AddCommand(
		installCmd(&cfg),
		uninstallCmd(),
		serviceCmd(),
		downloadCmd(&cfg),
		checkCmd(&cfg),
		versionCmd(),
	)

	return root
}

// ── install ───────────────────────────────────────────────────────────────────

func installCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install KubeSolo and configure the system service",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cfg)
		},
	}
	addInstallFlags(cmd, cfg)
	return cmd
}

func addInstallFlags(cmd *cobra.Command, cfg *config.Config) {
	f := cmd.Flags()

	f.StringVar(&cfg.Version, "version",
		envOr("KUBESOLO_VERSION", config.DefaultVersion),
		"KubeSolo version to install (e.g. v1.1.2)")

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
		"How to run KubeSolo: service (default), daemon, or foreground")

	f.StringVar(&cfg.Proxy, "proxy",
		os.Getenv("KUBESOLO_PROXY"),
		"HTTP/HTTPS proxy URL (injected into the service environment)")

	f.StringVar(&cfg.OfflineInstall, "offline-install",
		os.Getenv("KUBESOLO_OFFLINE_INSTALL"),
		"Path to a local tarball or binary to install instead of downloading")

	f.BoolVar(&cfg.InstallPrereqs, "install-prereqs",
		envBool("KUBESOLO_INSTALL_PREREQS", false),
		"Automatically install missing OS prerequisites (e.g. nftables on Alpine)")
}

func runInstall(cfg *config.Config) error {
	// ── detect host ───────────────────────────────────────────────────────────
	info, err := detect.Detect()
	if err != nil {
		return fmt.Errorf("system detection failed: %w", err)
	}
	log.Info().
		Str("arch", info.Arch).
		Str("libc", string(info.LibC)).
		Str("init", string(info.InitSystem)).
		Str("env", string(info.Environment)).
		Msg("host detected")

	// ── stop any running KubeSolo and clean up file conflicts ────────────────
	// This must happen before pre-flight checks so that ports held by a
	// previous KubeSolo installation are released before CheckPorts runs.
	// (Mirrors the bash installer's ordering: stop first, then check.)
	initBinary := initControlBinary(info.InitSystem)
	process.StopAll(initBinary)
	process.CleanupFileConflicts(cfg.Path)

	// ── pre-flight checks ─────────────────────────────────────────────────────
	if err := preflight.RunSuite(preflight.Suite(cfg.InstallPrereqs)); err != nil {
		return fmt.Errorf("pre-flight check failed: %w", err)
	}

	// ── install binary ────────────────────────────────────────────────────────
	log.Info().Msgf("installing KubeSolo %s...", cfg.Version)
	if err := download.Install(cfg.OfflineInstall, info.ArchiveName(cfg.Version), cfg.Version); err != nil {
		return fmt.Errorf("binary installation failed: %w", err)
	}

	// ── restore SELinux contexts if applicable ────────────────────────────────
	restoreSELinux(config.DefaultInstallPath)

	// ── set up service ────────────────────────────────────────────────────────
	mgr, err := service.New(info, cfg.RunMode)
	if err != nil {
		return err
	}
	cmdArgs := cfg.CmdArgs()
	log.Info().Msgf("setting up %s service (init: %s, mode: %s)...", config.AppName, info.InitSystem, cfg.RunMode)
	if err := mgr.Install(cfg, cmdArgs); err != nil {
		return fmt.Errorf("service setup failed: %w", err)
	}

	// ── post-install: kubeconfig merge ───────────────────────────────────────
	// Mirrors the bash script: poll for the kubeconfig (up to 30 s) and merge
	// it into the real user's ~/.kube/config. Runs synchronously so the
	// installer does not exit before the merge completes.
	if cfg.RunMode != config.RunModeForeground {
		printServiceHints(info.InitSystem)
		kubeconfig.MergeAfterStartup(cfg.Path)
	}

	log.Info().Msg("KubeSolo installation completed successfully")
	return nil
}

// ── uninstall ─────────────────────────────────────────────────────────────────

func uninstallCmd() *cobra.Command {
	var purge bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove KubeSolo and its service files",
		Long: `Stop KubeSolo, remove its service files, and delete the installed binary.

By default the data directory (` + config.DefaultPath + `) is left intact so that
cluster state is preserved. Use --purge to also remove it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := preflight.CheckRoot(); err != nil {
				return err
			}
			info, err := detect.Detect()
			if err != nil {
				return err
			}
			mgr, err := service.New(info, config.RunModeService)
			if err != nil {
				return err
			}

			// Stop service and remove service files
			if err := mgr.Uninstall(); err != nil {
				return err
			}

			// Remove the installed binary
			if err := os.Remove(config.DefaultInstallPath); err != nil && !os.IsNotExist(err) {
				log.Warn().Err(err).Msgf("could not remove binary at %s", config.DefaultInstallPath)
			} else {
				log.Info().Msgf("removed binary: %s", config.DefaultInstallPath)
			}

			// Optionally purge the data directory
			if purge {
				log.Warn().Msgf("purging data directory: %s", config.DefaultPath)
				if err := os.RemoveAll(config.DefaultPath); err != nil {
					log.Warn().Err(err).Msgf("could not fully remove data directory %s", config.DefaultPath)
				} else {
					log.Info().Msgf("data directory removed: %s", config.DefaultPath)
				}
			} else {
				log.Info().Msgf("data directory preserved: %s (use --purge to remove)", config.DefaultPath)
			}

			log.Info().Msg("KubeSolo uninstalled successfully")
			return nil
		},
	}

	cmd.Flags().BoolVar(&purge, "purge", false,
		"Also remove the data directory ("+config.DefaultPath+") — this deletes all cluster state")
	return cmd
}

// ── service management ────────────────────────────────────────────────────────

func serviceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "service <action>",
		Short: "Control the running KubeSolo service (replaces kubesolo-service.sh)",
		Long: `Control the KubeSolo system service.

Actions: start | stop | restart | status | logs | enable | disable`,
		Args:         cobra.ExactArgs(1),
		ValidArgs:    []string{"start", "stop", "restart", "status", "logs", "enable", "disable"},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := detect.Detect()
			if err != nil {
				return err
			}
			return runServiceAction(info.InitSystem, args[0])
		},
	}
}

func runServiceAction(init detect.InitSystem, action string) error {
	// "logs" is not a valid sub-command for any init system's control binary;
	// handle it explicitly per init system before the general dispatch below.
	if action == "logs" {
		switch init {
		case detect.InitSystemd:
			return runCmd("journalctl", "-u", config.AppName, "-f")
		case detect.InitOpenRC, detect.InitSysV:
			return runCmd("tail", "-f", "/var/log/messages")
		case detect.InitUpstart:
			return runCmd("tail", "-f", "/var/log/upstart/"+config.AppName+".log")
		default:
			return runCmd("tail", "-f", config.LogFile)
		}
	}

	switch init {
	case detect.InitSystemd:
		return runCmd("systemctl", action, config.AppName)
	case detect.InitOpenRC:
		if action == "enable" {
			return runCmd("rc-update", "add", config.AppName, "default")
		}
		if action == "disable" {
			return runCmd("rc-update", "del", config.AppName, "default")
		}
		return runCmd("rc-service", config.AppName, action)
	case detect.InitSysV:
		return runCmd("service", config.AppName, action)
	case detect.InitUpstart:
		if action == "status" {
			return runCmd("initctl", "status", config.AppName)
		}
		return runCmd("initctl", action, config.AppName)
	default:
		return fmt.Errorf(
			"service management not supported for init system %q — use direct process signals instead",
			init,
		)
	}
}

// ── download ──────────────────────────────────────────────────────────────────

func downloadCmd(cfg *config.Config) *cobra.Command {
	var dir string
	var targetArch string
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download the KubeSolo binary bundle for offline installation",
		Long: `Download the KubeSolo release tarball and installer binary to a local
directory for use on air-gapped machines.

By default the bundle targets the current host's architecture. Use --arch to
prepare a bundle for a different target, e.g. when downloading on an amd64
laptop for deployment to an arm64 device.

Examples:
  installer download --version=v1.1.2 --path=./offline-bundle
  installer download --version=v1.1.2 --path=./offline-bundle --arch=arm64
  installer download --version=v1.1.2 --path=./offline-bundle --arch=amd64-musl`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				dir = "."
			}
			var info *detect.SystemInfo
			var err error
			if targetArch != "" {
				info, err = detect.ForTarget(targetArch)
			} else {
				info, err = detect.Detect()
			}
			if err != nil {
				return err
			}
			return download.DownloadBundle(dir, info.ArchiveName(cfg.Version), info.InstallerName(), cfg.Version)
		},
	}
	cmd.Flags().StringVar(&cfg.Version, "version",
		envOr("KUBESOLO_VERSION", config.DefaultVersion),
		"Version to download")
	cmd.Flags().StringVar(&dir, "path", ".", "Directory to download files into")
	cmd.Flags().StringVar(&targetArch, "arch", "",
		"Target architecture for the bundle (default: current host).\n"+
			"Valid values: amd64, arm64, arm, riscv64, amd64-musl, arm64-musl")
	return cmd
}

// ── preflight check only ──────────────────────────────────────────────────────

func checkCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Run pre-flight checks without installing",
		Long:  `Validates that this host meets all requirements for KubeSolo. Exits 0 on success.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := detect.Detect()
			if err != nil {
				return err
			}
			log.Info().
				Str("arch", info.Arch).
				Str("libc", string(info.LibC)).
				Str("init", string(info.InitSystem)).
				Str("env", string(info.Environment)).
				Msg("host detected")
			return preflight.RunSuite(preflight.Suite(cfg.InstallPrereqs))
		},
	}
}

// ── version ───────────────────────────────────────────────────────────────────

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print installer version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("kubesolo-installer %s (commit %s, built %s)\n", Version, Commit, BuildDate)
		},
	}
}


// ── helpers ───────────────────────────────────────────────────────────────────

func configureLogging(debug bool) {
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "2006/01/02 03:04PM",
	})
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	switch os.Getenv(key) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return fallback
}

// initControlBinary returns the path to the init system's service control
// binary so process.StopViaInitSystem can invoke a graceful shutdown.
// It uses exec.LookPath first (respects $PATH) and falls back to a list of
// known absolute locations, since the binary may be in /usr/bin on some
// distros and /bin or /sbin on others.
func initControlBinary(init detect.InitSystem) string {
	var candidates []string
	switch init {
	case detect.InitSystemd:
		candidates = []string{"systemctl", "/usr/bin/systemctl", "/bin/systemctl"}
	case detect.InitOpenRC:
		candidates = []string{"rc-service", "/sbin/rc-service", "/usr/sbin/rc-service"}
	case detect.InitSysV:
		candidates = []string{"service", "/usr/sbin/service", "/sbin/service"}
	default:
		return ""
	}
	if p, err := exec.LookPath(candidates[0]); err == nil {
		return p
	}
	for _, p := range candidates[1:] {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// restoreSELinux restores SELinux file contexts for path if restorecon exists.
func restoreSELinux(path string) {
	for _, dir := range []string{"/usr/sbin", "/sbin"} {
		rc := dir + "/restorecon"
		if _, err := os.Stat(rc); err == nil {
			log.Info().Msg("restoring SELinux file context for installed binary...")
			if err := runCmd(rc, "-v", path); err != nil {
				log.Warn().Err(err).Msg("restorecon returned an error (may be normal in permissive mode)")
			}
			return
		}
	}
}

// runCmd executes a command, sending combined stdout/stderr to the zerolog logger.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// printServiceHints logs management commands appropriate for the detected init system.
func printServiceHints(init detect.InitSystem) {
	app := config.AppName
	switch init {
	case detect.InitSystemd:
		log.Info().Msgf("status: systemctl status %s", app)
		log.Info().Msgf("logs:   journalctl -u %s -f", app)
	case detect.InitOpenRC:
		log.Info().Msgf("status: rc-service %s status", app)
		log.Info().Msgf("logs:   tail -f /var/log/messages")
	case detect.InitSysV:
		log.Info().Msgf("status: service %s status", app)
		log.Info().Msgf("logs:   tail -f /var/log/syslog")
	default:
		log.Info().Msgf("logs:   tail -f %s", config.LogFile)
	}
}
