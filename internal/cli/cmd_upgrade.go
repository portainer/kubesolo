package cli

import (
	"fmt"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/download"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/process"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func upgradeCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade KubeSolo to a newer version",
		Long: `Download a newer KubeSolo release, stop the running service, replace the
binary, and restart the service. Cluster state (certificates, database) is
preserved across the upgrade.

Examples:
  sudo kubesoloctl upgrade --version=v1.1.5
  sudo kubesoloctl upgrade --version=v1.1.5 --offline-install=/tmp/kubesolo-v1.1.5-linux-amd64.tar.gz`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cfg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Version, "version", "", "KubeSolo version to upgrade to (required)")
	_ = cmd.MarkFlagRequired("version")
	f.StringVar(&cfg.OfflineInstall, "offline-install", "",
		"Path to a local tarball or binary to install instead of downloading")
	return cmd
}

func runUpgrade(cfg *config.Config) error {
	if err := preflight.CheckRoot(); err != nil {
		return err
	}

	info, err := detect.Detect()
	if err != nil {
		return fmt.Errorf("system detection failed: %w", err)
	}

	// Stop the running service
	log.Info().Msg("stopping KubeSolo service...")
	initBinary := initControlBinary(info.InitSystem)
	process.StopAll(initBinary)

	// Replace the binary
	log.Info().Msgf("installing KubeSolo %s...", cfg.Version)
	if err := download.Install(cfg.OfflineInstall, info.ArchiveName(cfg.Version), cfg.Version); err != nil {
		return fmt.Errorf("binary installation failed: %w", err)
	}

	restoreSELinux(config.DefaultInstallPath)

	// Restart the service
	log.Info().Msg("restarting KubeSolo service...")
	if err := runServiceAction(info.InitSystem, "start"); err != nil {
		return fmt.Errorf("failed to restart service: %w", err)
	}

	log.Info().Msgf("KubeSolo upgraded to %s successfully", cfg.Version)
	return nil
}
