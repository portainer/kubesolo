package cli

import (
	"fmt"
	"os"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/spf13/cobra"
)

func logsCmd() *cobra.Command {
	var (
		name   string
		follow bool
		tail   string
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Print what KubeSolo logged in a container instance",
		Long: `Print what KubeSolo logged in a container instance: its components, and the
errors that keep a cluster from coming up. On a host install, KubeSolo logs to
the init system instead (for example journalctl -u kubesolo).

Examples:
  kubesoloctl logs --tail 100
  kubesoloctl logs --name dev -f`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !service.ContainerExists(name) {
				return fmt.Errorf("container %q not found — logs works on container instances; on a host install use the init system's log, e.g. journalctl -u kubesolo", service.ContainerNameFor(name))
			}
			return service.StreamLogs(name, follow, tail, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&name, "name", envOr("KUBESOLO_NAME", config.AppName), "Name of the KubeSolo instance")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep printing what KubeSolo logs")
	cmd.Flags().StringVar(&tail, "tail", "all", "Number of lines to show from the end, or all")
	return cmd
}
