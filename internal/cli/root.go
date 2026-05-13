// Package cli implements the kubesoloctl command-line interface.
//
// The only exported symbol is Execute, which builds and runs the full
// cobra command tree. cmd/kubesoloctl/main.go is a thin wrapper that
// calls Execute with version metadata injected at build time.
package cli

import (
	"os"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/spf13/cobra"
)

// version metadata — set via SetVersionInfo before Execute.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// SetVersionInfo records build-time version metadata for the version command.
func SetVersionInfo(v, c, d string) {
	version = v
	commit = c
	buildDate = d
}

// Execute builds the cobra command tree and runs it.
func Execute() error {
	return rootCmd().Execute()
}

func rootCmd() *cobra.Command {
	var cfg config.Config

	root := &cobra.Command{
		Use:   "kubesoloctl",
		Short: "KubeSolo management CLI — single-node Kubernetes, anywhere",
		Long: `kubesoloctl manages the lifecycle of a KubeSolo single-node Kubernetes cluster.

Examples:
  # Standard install (auto-detects init system):
  sudo kubesoloctl install

  # Specify version and Portainer edge credentials:
  sudo kubesoloctl install --version=v1.1.5 --portainer-edge-id=ID --portainer-edge-key=KEY

  # Air-gap install from a local archive:
  sudo kubesoloctl install --offline-install=/tmp/kubesolo-v1.1.5-linux-amd64.tar.gz

  # Upgrade to a newer version:
  sudo kubesoloctl upgrade --version=v1.1.5

  # Check pre-flight conditions without installing:
  kubesoloctl check`,
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			configureLogging(cfg.Debug)
		},
	}

	root.AddCommand(
		installCmd(&cfg),
		uninstallCmd(),
		upgradeCmd(&cfg),
		kubeconfigCmd(),
		downloadCmd(&cfg),
		checkCmd(&cfg),
		resetCmd(),
		completionCmd(),
		versionCmd(),
	)

	return root
}

// exitOnError is a convenience for main to exit with code 1 on error.
func ExitOnError(err error) {
	if err != nil {
		os.Exit(1)
	}
}
