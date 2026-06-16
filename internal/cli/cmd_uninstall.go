package cli

import (
	"fmt"
	"os"
	"runtime"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/process"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/spf13/cobra"
)

func uninstallCmd() *cobra.Command {
	var purge bool
	var removeKubeconfig bool
	var name string

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove KubeSolo and its service files",
		Long: `Stop KubeSolo, remove its service files, and delete the installed binary.

By default the data directory (` + config.DefaultPath + `) is left intact so that
cluster state is preserved. Use --purge to also remove it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(name, purge, removeKubeconfig)
		},
	}

	cmd.Flags().StringVar(&name, "name", envOr("KUBESOLO_NAME", config.AppName),
		"Name of the KubeSolo instance to uninstall (default: kubesolo)")
	cmd.Flags().BoolVar(&purge, "purge", false,
		"Also remove the data directory ("+config.DefaultPath+") — this deletes all cluster state")
	cmd.Flags().BoolVar(&removeKubeconfig, "remove-kubeconfig", false,
		"Remove the KubeSolo context, cluster, and user entries from ~/.kube/config")
	return cmd
}

func runUninstall(name string, purge, removeKubeconfig bool) error {
	p := ui.New()
	p.Header("uninstall")

	if runtime.GOOS == "darwin" {
		return runContainerUninstall(p, name, purge, removeKubeconfig)
	}

	if err := preflight.CheckRoot(); err != nil {
		return p.Fail("root check", err)
	}

	info, err := detect.Detect()
	if err != nil {
		return p.Fail("system detection", err)
	}

	// ── Stop service ──────────────────────────────────────────────────────────
	p.Step("Stopping KubeSolo")
	process.StopAll(initControlBinary(info.InitSystem))
	p.OK("KubeSolo stopped", "")

	// ── Remove service files ──────────────────────────────────────────────────
	p.Step(fmt.Sprintf("Removing %s service", info.InitSystem))
	mgr, err := service.New(info, config.RunModeService, "")
	if err != nil {
		return p.Fail("service removal", err)
	}
	if err := mgr.Uninstall(); err != nil {
		return p.Fail("service removal", err)
	}
	p.OK(fmt.Sprintf("%s service removed", info.InitSystem), "")

	// ── Remove binary ─────────────────────────────────────────────────────────
	p.Step("Removing binary")
	if err := os.Remove(config.DefaultInstallPath); err != nil && !os.IsNotExist(err) {
		p.Warn("could not remove binary: " + err.Error())
	} else {
		p.OK("Binary removed", config.DefaultInstallPath)
	}

	// ── Purge data directory ──────────────────────────────────────────────────
	if purge {
		p.Step("Removing data directory")
		unmountDataDir(config.DefaultPath)
		if err := os.RemoveAll(config.DefaultPath); err != nil {
			p.Warn("could not fully remove data directory: " + err.Error())
		} else {
			p.OK("Data directory removed", config.DefaultPath)
		}
	} else {
		p.Info("Data directory preserved: " + config.DefaultPath + " (use --purge to remove)")
	}

	// ── Clean kubeconfig ──────────────────────────────────────────────────────
	if removeKubeconfig {
		p.Step("Cleaning kubeconfig")
		kubeconfig.RemoveFromUserConfig(name)
		p.OK("Kubeconfig entries removed", "")
	}

	p.Done("KubeSolo uninstalled.")
	return nil
}

func runContainerUninstall(p *ui.Printer, name string, purge, removeKubeconfig bool) error {
	info, err := detect.Detect()
	if err != nil {
		return p.Fail("system detection", err)
	}

	// ── Stop and remove container ─────────────────────────────────────────────
	p.Step("Removing KubeSolo container")
	mgr, err := service.New(info, config.RunModeContainer, name)
	if err != nil {
		return p.Fail("container removal", err)
	}
	if err := mgr.Uninstall(); err != nil {
		return p.Fail("container removal", err)
	}
	p.OK("Container removed", name)

	// ── Purge Docker volume ───────────────────────────────────────────────────
	volumeName := service.ContainerNameFor(name) + "-data"
	if purge {
		p.Step("Removing cluster data volume")
		if err := service.RemoveContainerVolume(name); err != nil {
			p.Warn("could not remove volume: " + err.Error())
		} else {
			p.OK("Volume removed", volumeName)
		}
	} else {
		p.Info("Cluster data preserved in Docker volume " + volumeName + " (use --purge to remove)")
	}

	// ── Clean kubeconfig ──────────────────────────────────────────────────────
	if removeKubeconfig {
		p.Step("Cleaning kubeconfig")
		kubeconfig.RemoveFromUserConfig(name)
		p.OK("Kubeconfig entries removed", "")
	}

	p.Done("KubeSolo uninstalled.")
	return nil
}
