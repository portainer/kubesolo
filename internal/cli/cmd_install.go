package cli

import (
	"fmt"
	"os"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/download"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/process"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

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

	// Stop any running KubeSolo and clean up file conflicts before pre-flight
	// checks so that ports held by a previous installation are released.
	initBinary := initControlBinary(info.InitSystem)
	process.StopAll(initBinary)
	process.CleanupFileConflicts(cfg.Path)

	if err := preflight.RunSuite(preflight.Suite(cfg.InstallPrereqs, cfg.PprofServer)); err != nil {
		return fmt.Errorf("pre-flight check failed: %w", err)
	}

	log.Info().Msgf("installing KubeSolo %s...", cfg.Version)
	if err := download.Install(cfg.OfflineInstall, info.ArchiveName(cfg.Version), cfg.Version); err != nil {
		return fmt.Errorf("binary installation failed: %w", err)
	}

	restoreSELinux(config.DefaultInstallPath)

	mgr, err := service.New(info, cfg.RunMode)
	if err != nil {
		return err
	}
	cmdArgs := cfg.CmdArgs()
	log.Info().Msgf("setting up %s service (init: %s, mode: %s)...", config.AppName, info.InitSystem, cfg.RunMode)
	if err := mgr.Install(cfg, cmdArgs); err != nil {
		return fmt.Errorf("service setup failed: %w", err)
	}

	if cfg.RunMode != config.RunModeForeground {
		printServiceHints(info.InitSystem)
		kubeconfig.MergeAfterStartup(cfg.Path)
	}

	log.Info().Msg("KubeSolo installation completed successfully")
	return nil
}
