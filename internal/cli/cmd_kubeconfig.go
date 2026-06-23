package cli

import (
	"fmt"
	"os"
	"path/filepath"
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

  # Fetch from a running KubeSolo container and merge into ~/.kube/config:
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
	var dataPath string
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Merge the admin kubeconfig into ~/.kube/config",
		Long: `Merges the KubeSolo admin kubeconfig into the local user's ~/.kube/config,
backing up any existing config first.

In container mode (a running KubeSolo container is detected), the kubeconfig is
copied out of the container. Otherwise it is read from the local KubeSolo data
directory (a host install running as a system service).

Examples:
  kubesoloctl kubeconfig fetch
  kubesoloctl kubeconfig fetch --path /var/lib/kubesolo   (host install)
  kubesoloctl kubeconfig fetch --container my-kubesolo    (container mode)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ui.New()
			p.Header("kubeconfig fetch")

			p.Step("Merging kubeconfig")
			if containerModeActiveFor(containerName) {
				if err := kubeconfig.FetchAndMergeFromContainer(containerName, socketPath); err != nil {
					return p.Fail("kubeconfig fetch", err)
				}
			} else {
				if err := kubeconfig.MergeFromDisk(dataPath); err != nil {
					return p.Fail("kubeconfig fetch", err)
				}
			}
			p.OK("Kubeconfig merged", "~/.kube/config")

			p.Done("Kubeconfig ready")
			return nil
		},
	}
	cmd.Flags().StringVar(&containerName, "container", service.ContainerNameFor(envOr("KUBESOLO_NAME", config.AppName)),
		"Container name or ID running KubeSolo (container mode)")
	cmd.Flags().StringVar(&socketPath, "socket", "",
		"Container engine socket path (default: DOCKER_HOST env or /var/run/docker.sock) (container mode)")
	cmd.Flags().StringVar(&dataPath, "path", config.DefaultPath,
		"KubeSolo data directory (host install)")
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

In container mode (a running KubeSolo container is detected), the kubeconfig is
read directly from the container. Otherwise it is read from the local KubeSolo
data directory.

Examples:
  kubesoloctl kubeconfig view
  kubesoloctl kubeconfig view --container my-kubesolo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if containerModeActiveFor(containerName) {
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
		"Container name or ID running KubeSolo (container mode)")
	cmd.Flags().StringVar(&socketPath, "socket", "",
		"Container engine socket path (default: DOCKER_HOST env or /var/run/docker.sock) (container mode)")
	cmd.Flags().StringVar(&dataPath, "path", config.DefaultPath,
		"KubeSolo data directory (host install)")
	return cmd
}

func runKubeconfig(dataPath, output string) error {
	if containerModeActive(config.AppName) {
		return fmt.Errorf("KubeSolo is running in container mode — use: kubesoloctl kubeconfig view")
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
