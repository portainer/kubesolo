package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/process"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func resetCmd() *cobra.Command {
	var force bool
	var dataPath string
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset KubeSolo cluster to a clean state",
		Long: `Stop the KubeSolo service and wipe all cluster state (database, certificates,
CNI configuration) while preserving the installed binary and service files.
After a reset the service is restarted to initialize a fresh cluster.

This is a destructive operation — use --force to skip the confirmation prompt.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReset(dataPath, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	cmd.Flags().StringVar(&dataPath, "path", config.DefaultPath, "KubeSolo data directory")
	return cmd
}

func runReset(dataPath string, force bool) error {
	if err := preflight.CheckRoot(); err != nil {
		return err
	}

	if !force {
		fmt.Printf("This will delete all cluster state in %s.\n", dataPath)
		fmt.Print("Are you sure? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			log.Info().Msg("reset cancelled")
			return nil
		}
	}

	info, err := detect.Detect()
	if err != nil {
		return fmt.Errorf("system detection failed: %w", err)
	}

	// Stop the service
	log.Info().Msg("stopping KubeSolo service...")
	initBinary := initControlBinary(info.InitSystem)
	process.StopAll(initBinary)

	// Remove the data directory
	log.Warn().Msgf("removing cluster state: %s", dataPath)
	if err := os.RemoveAll(dataPath); err != nil {
		return fmt.Errorf("failed to remove data directory: %w", err)
	}

	// Restart the service to initialize fresh state
	log.Info().Msg("starting KubeSolo service (fresh cluster)...")
	if err := runServiceAction(info.InitSystem, "start"); err != nil {
		log.Warn().Err(err).Msg("could not restart service — start it manually")
	}

	log.Info().Msg("KubeSolo reset complete — fresh cluster initializing")
	return nil
}
