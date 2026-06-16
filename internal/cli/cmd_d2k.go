package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/spf13/cobra"
)

func d2kCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "d2k",
		Short: "Manage the D2K Docker-to-Kubernetes API translator",
		Long: `Commands for managing the D2K Docker-to-Kubernetes translator running inside
a KubeSolo container. D2K exposes a Docker-compatible API on a random host port
backed by mTLS — use 'fetch' to set up the Docker context and 'view' to inspect
the current configuration.

Requires the KubeSolo instance to have been installed with --d2k.`,
	}
	cmd.AddCommand(d2kFetchCmd())
	cmd.AddCommand(d2kViewCmd())
	return cmd
}

func d2kFetchCmd() *cobra.Command {
	var name string
	var socketPath string
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch D2K mTLS credentials from a container and create a Docker context",
		Long: `Copies the D2K CA certificate and client keypair out of a running KubeSolo
container, writes them to ~/.docker/d2k/<name>/, and creates (or updates)
a Docker context named <name> pointing to the container's ephemeral D2K port.

The host port is random (assigned by Docker at install time), so re-run this
command after each upgrade or reset to keep the Docker context up to date.

Examples:
  # Set up Docker context for the default KubeSolo instance:
  kubesoloctl d2k fetch

  # Set up Docker context for a named instance:
  kubesoloctl d2k fetch --name prod

  # Use a non-default Docker socket:
  kubesoloctl d2k fetch --socket /run/user/1000/docker.sock`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ui.New()
			p.Header("d2k fetch")

			p.Step("Discovering D2K port")
			port, err := service.GetContainerD2KPort(name)
			if err != nil {
				return p.Fail("D2K port discovery", err)
			}
			p.OK("D2K port discovered", fmt.Sprintf("127.0.0.1:%d", port))

			p.Step("Fetching credentials")
			cname := service.ContainerNameFor(name)
			if err := kubeconfig.FetchD2KFromContainer(cname, name, socketPath, port); err != nil {
				return p.Fail("credential fetch", err)
			}
			p.OK("Docker context ready", name)

			p.Done(fmt.Sprintf("D2K context %q configured", name))
			p.Section("Next steps")
			p.Info(fmt.Sprintf("docker --context %s ps", name))
			p.Info(fmt.Sprintf("docker context use %s   # set as default", name))
			p.Info("")
			p.Info("Re-run after upgrade or reset — the host port changes")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", envOr("KUBESOLO_NAME", config.AppName),
		"KubeSolo instance name (default: kubesolo)")
	cmd.Flags().StringVar(&socketPath, "socket", "",
		"Docker socket path (default: DOCKER_HOST env or /var/run/docker.sock)")
	return cmd
}

func d2kViewCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Print the Docker context configuration for this KubeSolo instance",
		Long: `Prints the Docker context entry for the named KubeSolo D2K instance,
showing the mTLS endpoint and certificate paths.

Examples:
  kubesoloctl d2k view
  kubesoloctl d2k view --name prod`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runD2KView(name)
		},
	}
	cmd.Flags().StringVar(&name, "name", envOr("KUBESOLO_NAME", config.AppName),
		"KubeSolo instance name (default: kubesolo)")
	return cmd
}

func runD2KView(name string) error {
	if name == "" {
		name = config.AppName
	}

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker command line tool not found — install it, then run: kubesoloctl d2k fetch")
	}

	out, err := exec.Command(dockerPath, "context", "inspect", name).Output()
	if err != nil {
		return fmt.Errorf("Docker context %q not found — run: kubesoloctl d2k fetch --name %s", name, name)
	}

	_, err = os.Stdout.Write(out)
	return err
}
