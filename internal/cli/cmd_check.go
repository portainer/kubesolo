package cli

import (
	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

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
			return preflight.RunSuite(preflight.Suite(cfg.InstallPrereqs, cfg.PprofServer))
		},
	}
}
