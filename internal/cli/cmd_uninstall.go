package cli

import (
	"os"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/process"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func uninstallCmd() *cobra.Command {
	var purge bool
	var removeKubeconfig bool

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
			process.StopAll(initControlBinary(info.InitSystem))
			mgr, err := service.New(info, config.RunModeService)
			if err != nil {
				return err
			}

			if err := mgr.Uninstall(); err != nil {
				return err
			}

			if err := os.Remove(config.DefaultInstallPath); err != nil && !os.IsNotExist(err) {
				log.Warn().Err(err).Msgf("could not remove binary at %s", config.DefaultInstallPath)
			} else {
				log.Info().Msgf("removed binary: %s", config.DefaultInstallPath)
			}

			if purge {
				log.Warn().Msgf("purging data directory: %s", config.DefaultPath)
				unmountDataDir(config.DefaultPath)
				if err := os.RemoveAll(config.DefaultPath); err != nil {
					log.Warn().Err(err).Msgf("could not fully remove data directory %s", config.DefaultPath)
				} else {
					log.Info().Msgf("data directory removed: %s", config.DefaultPath)
				}
			} else {
				log.Info().Msgf("data directory preserved: %s (use --purge to remove)", config.DefaultPath)
			}

			if removeKubeconfig {
				kubeconfig.RemoveFromUserConfig()
			}

			log.Info().Msg("KubeSolo uninstalled successfully")
			return nil
		},
	}

	cmd.Flags().BoolVar(&purge, "purge", false,
		"Also remove the data directory ("+config.DefaultPath+") — this deletes all cluster state")
	cmd.Flags().BoolVar(&removeKubeconfig, "remove-kubeconfig", false,
		"Remove the KubeSolo context, cluster, and user entries from ~/.kube/config")
	return cmd
}

