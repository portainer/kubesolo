package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func kubeconfigCmd() *cobra.Command {
	var output string
	var dataPath string
	cmd := &cobra.Command{
		Use:   "kubeconfig",
		Short: "Export or merge the admin kubeconfig",
		Long: `Print the admin kubeconfig to stdout, or write it to a target file.

Examples:
  # Print kubeconfig to stdout:
  kubesoloctl kubeconfig

  # Write to a specific file:
  kubesoloctl kubeconfig --output=~/.kube/config`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKubeconfig(dataPath, output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "",
		"Write kubeconfig to this file. Empty means stdout.")
	cmd.Flags().StringVar(&dataPath, "path", config.DefaultPath, "KubeSolo data directory")
	return cmd
}

func runKubeconfig(dataPath, output string) error {
	kcPath := kubeconfig.KubeSoloKubeconfigPath(dataPath)
	data, err := os.ReadFile(kcPath)
	if err != nil {
		return fmt.Errorf("could not read kubeconfig at %s: %w", kcPath, err)
	}

	if output == "" {
		_, err = os.Stdout.Write(data)
		return err
	}

	// Expand ~ prefix
	if strings.HasPrefix(output, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("could not resolve home directory: %w", err)
		}
		output = filepath.Join(home, output[2:])
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return fmt.Errorf("could not create directory: %w", err)
	}

	if err := os.WriteFile(output, data, 0o600); err != nil {
		return fmt.Errorf("could not write kubeconfig: %w", err)
	}

	log.Info().Msgf("kubeconfig written to %s", output)
	return nil
}
