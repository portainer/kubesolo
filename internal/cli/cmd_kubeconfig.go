package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
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
  kubesoloctl kubeconfig --output=~/.kube/config

  # Fetch from a running Docker container and merge into ~/.kube/config:
  kubesoloctl kubeconfig fetch`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKubeconfig(dataPath, output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "",
		"Write kubeconfig to this file. Empty means stdout.")
	cmd.Flags().StringVar(&dataPath, "path", config.DefaultPath, "KubeSolo data directory")
	cmd.AddCommand(kubeconfigFetchCmd())
	cmd.AddCommand(kubeconfigViewCmd())
	return cmd
}

func kubeconfigFetchCmd() *cobra.Command {
	var containerName string
	var socketPath string
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch kubeconfig from a Docker container and merge into ~/.kube/config",
		Long: `Copies /var/lib/kubesolo/pki/admin/admin.kubeconfig out of a running
KubeSolo Docker container using the Docker SDK and merges it into the local
user's ~/.kube/config (backed up first if it already exists).

Examples:
  # Fetch from the default container name:
  kubesoloctl kubeconfig fetch

  # Fetch from a container with a custom name or ID:
  kubesoloctl kubeconfig fetch --container my-kubesolo

  # Use a non-default Docker socket:
  kubesoloctl kubeconfig fetch --socket /run/user/1000/docker.sock`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ui.New()
			p.Header("kubeconfig fetch")

			p.Step("Fetching and merging kubeconfig")
			if err := kubeconfig.FetchAndMergeFromContainer(containerName, socketPath); err != nil {
				return p.Fail("kubeconfig fetch", err)
			}
			p.OK("Kubeconfig merged", "~/.kube/config")

			p.Done("Kubeconfig ready")
			return nil
		},
	}
	cmd.Flags().StringVar(&containerName, "container", service.ContainerNameFor(envOr("KUBESOLO_NAME", config.AppName)),
		"Docker container name or ID running KubeSolo")
	cmd.Flags().StringVar(&socketPath, "socket", "",
		"Docker socket path (default: DOCKER_HOST env or /var/run/docker.sock)")
	return cmd
}

func kubeconfigViewCmd() *cobra.Command {
	var containerName string
	var socketPath string
	var dataPath string
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Print the admin kubeconfig to stdout",
		Long: `Print the raw admin kubeconfig to stdout.

On macOS the kubeconfig is read directly from the running KubeSolo Docker
container. On Linux it is read from the KubeSolo data directory.

Examples:
  kubesoloctl kubeconfig view
  kubesoloctl kubeconfig view --container my-kubesolo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "darwin" {
				data, err := kubeconfig.GetFromContainer(containerName, socketPath)
				if err != nil {
					return err
				}
				_, err = os.Stdout.Write(data)
				return err
			}
			kcPath := kubeconfig.KubeSoloKubeconfigPath(dataPath)
			data, err := os.ReadFile(kcPath)
			if err != nil {
				return fmt.Errorf("could not read kubeconfig at %s: %w", kcPath, err)
			}
			_, err = os.Stdout.Write(data)
			return err
		},
	}
	cmd.Flags().StringVar(&containerName, "container", service.ContainerNameFor(envOr("KUBESOLO_NAME", config.AppName)),
		"Docker container name or ID running KubeSolo (macOS only)")
	cmd.Flags().StringVar(&socketPath, "socket", "",
		"Docker socket path (default: DOCKER_HOST env or /var/run/docker.sock) (macOS only)")
	cmd.Flags().StringVar(&dataPath, "path", config.DefaultPath,
		"KubeSolo data directory (Linux only)")
	return cmd
}

func runKubeconfig(dataPath, output string) error {
	if runtime.GOOS == "darwin" {
		return fmt.Errorf("on macOS, KubeSolo runs in a container — use: kubesoloctl kubeconfig view")
	}
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
