package cli

import (
	"fmt"
	"time"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/download"
	"github.com/portainer/kubesolo/internal/cli/kubeconfig"
	"github.com/portainer/kubesolo/internal/cli/preflight"
	"github.com/portainer/kubesolo/internal/cli/process"
	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/spf13/cobra"
)

func upgradeCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade KubeSolo to a newer version",
		Long: `Download a newer KubeSolo release, stop the running service, replace the
binary, and restart the service. Cluster state (certificates, database) is
preserved across the upgrade.

Examples:
  sudo kubesoloctl upgrade --version=v1.1.7
  sudo kubesoloctl upgrade --version=v1.1.7 --offline-install=/tmp/kubesolo-v1.1.7-linux-amd64.tar.gz`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cfg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Version, "version", "", "KubeSolo version to upgrade to (required)")
	_ = cmd.MarkFlagRequired("version")
	f.StringVar(&cfg.OfflineInstall, "offline-install", "",
		"Path to a local tarball or binary to install instead of downloading")
	f.StringVar(&cfg.Name, "name", envOr("KUBESOLO_NAME", config.AppName),
		"Name of the KubeSolo instance to upgrade (default: kubesolo)")
	return cmd
}

func runUpgrade(cfg *config.Config) error {
	p := ui.New()
	p.Header("upgrade")

	if containerModeActive(cfg.Name) {
		return runContainerUpgrade(p, cfg)
	}

	if err := preflight.CheckRoot(); err != nil {
		return p.Fail("root check", err)
	}

	// ── Detect system ─────────────────────────────────────────────────────────
	p.Step("Detecting system")
	info, err := detect.Detect()
	if err != nil {
		return p.Fail("system detection", err)
	}
	p.OK("System detected", fmt.Sprintf("%s · %s · %s", info.Arch, info.LibC, info.InitSystem))

	// ── Stop service ──────────────────────────────────────────────────────────
	p.Step("Stopping KubeSolo")
	process.StopAll(initControlBinary(info.InitSystem))
	p.OK("KubeSolo stopped", "")

	// ── Replace binary ────────────────────────────────────────────────────────
	p.Step(fmt.Sprintf("Installing KubeSolo %s", cfg.Version))
	if err := download.Install(cfg.OfflineInstall, info.ArchiveName(cfg.Version), cfg.Version); err != nil {
		return p.Fail("binary installation", err)
	}
	restoreSELinux(config.DefaultInstallPath)
	p.OK(fmt.Sprintf("KubeSolo %s installed", cfg.Version), config.DefaultInstallPath)

	// ── Restart service ───────────────────────────────────────────────────────
	p.Step(fmt.Sprintf("Restarting %s service", info.InitSystem))
	if err := runServiceAction(info.InitSystem, "start"); err != nil {
		return p.Fail("service restart", err)
	}
	p.OK("Service restarted", "")

	p.Done(fmt.Sprintf("KubeSolo upgraded to %s", cfg.Version))
	return nil
}

func runContainerUpgrade(p *ui.Printer, cfg *config.Config) error {
	// ── Retrieve current container CMD before replacing it ────────────────────
	p.Step("Inspecting running container")
	oldArgs, err := service.ContainerArgs(cfg.Name)
	if err != nil {
		return p.Fail("container inspect", err)
	}
	// Container mode always runs with upstream defaults (--full); ensure it
	// survives upgrades of containers created before that became the default.
	oldArgs = ensureArg(oldArgs, "--full")
	p.OK("Current configuration retrieved", config.AppName)

	// ── Pull new image, stop old container, start fresh ───────────────────────
	p.Step(fmt.Sprintf("Upgrading KubeSolo container to %s", cfg.Version))

	info, err := detect.Detect()
	if err != nil {
		return p.Fail("system detection", err)
	}
	mgr, err := service.New(info, config.RunModeContainer, cfg.Name)
	if err != nil {
		return p.Fail("container upgrade", err)
	}

	// Install with the new version but preserve the original runtime flags.
	if err := mgr.Install(cfg, oldArgs); err != nil {
		return p.Fail("container upgrade", err)
	}
	p.OK(fmt.Sprintf("KubeSolo %s container running", cfg.Version), cfg.Name)

	// Update kubeconfig with the new container's ephemeral port.
	p.Step("Updating kubeconfig")
	cname := service.ContainerNameFor(cfg.Name)
	if port, err := service.GetContainerAPIPort(cfg.Name); err == nil {
		apiAddr := fmt.Sprintf("127.0.0.1:%d", port)
		if data, err := kubeconfig.WaitForContainerKubeconfig(cname, ""); err == nil {
			kubeconfig.MergeContainerKubeconfig(data, cfg.Name, "https://"+apiAddr)
		}
		if err := kubeconfig.WaitForAPIServer(apiAddr, 120*time.Second); err != nil {
			p.Warn(fmt.Sprintf("API server not yet ready at https://%s", apiAddr))
		} else {
			p.OK("API server ready", "https://"+apiAddr)
		}
	} else {
		p.Warn("could not discover container API port — run: kubesoloctl kubeconfig fetch")
	}

	p.Done(fmt.Sprintf("KubeSolo upgraded to %s", cfg.Version))
	return nil
}
