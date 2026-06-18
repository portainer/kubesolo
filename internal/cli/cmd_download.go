package cli

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/download"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/spf13/cobra"
)

func downloadCmd(cfg *config.Config) *cobra.Command {
	var dir string
	var targetArch string

	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download the KubeSolo binary bundle for offline installation",
		Long: `Download the KubeSolo release tarball and kubesoloctl binary to a local
directory for use on air-gapped machines.

By default the bundle targets the current host's architecture. Use --arch to
prepare a bundle for a different target, e.g. when downloading on an amd64
laptop for deployment to an arm64 device.

Examples:
  kubesoloctl download --version=v1.1.7 --path=./offline-bundle
  kubesoloctl download --version=v1.1.7 --path=./offline-bundle --arch=arm64
  kubesoloctl download --version=v1.1.7 --path=./offline-bundle --arch=amd64-musl`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := ui.New()
			p.Header("download")

			if dir == "" {
				dir = "."
			}
			// KubeSolo has no macOS binaries — the target arch must be specified
			// explicitly when downloading from a Mac for deployment to a Linux device.
			if targetArch == "" && runtime.GOOS == "darwin" {
				return fmt.Errorf("on macOS, KubeSolo has no native binaries — specify the target Linux arch with --arch (e.g. --arch=amd64 or --arch=arm64)")
			}

			p.Step("Resolving target architecture")
			var info *detect.SystemInfo
			var err error
			if targetArch != "" {
				info, err = detect.ForTarget(targetArch)
			} else {
				info, err = detect.Detect()
			}
			if err != nil {
				return p.Fail("architecture detection", err)
			}
			p.OK("Target resolved", info.ArchiveName(cfg.Version))

			archiveName := info.ArchiveName(cfg.Version)
			p.Step(fmt.Sprintf("Downloading KubeSolo %s", cfg.Version))
			if err := download.DownloadBundle(dir, archiveName, cfg.Version); err != nil {
				return p.Fail("download", err)
			}

			p.Done(fmt.Sprintf("Bundle ready in %s", dir))
			p.Section("Next steps")
			p.Info("Transfer the files to the target machine, then:")
			p.Info(fmt.Sprintf("  sudo ./kubesoloctl install --offline-install=%s", filepath.Join(dir, archiveName)))
			return nil
		},
	}

	cmd.Flags().StringVar(&cfg.Version, "version",
		envOr("KUBESOLO_VERSION", config.DefaultVersion),
		"Version to download")
	cmd.Flags().StringVar(&dir, "path", ".", "Directory to download files into")
	cmd.Flags().StringVar(&targetArch, "arch", "",
		"Target architecture for the bundle (default: current host).\n"+
			"Valid values: amd64, arm64, arm, riscv64, amd64-musl, arm64-musl.\n"+
			"The -musl suffix selects the musl KubeSolo archive; the kubesoloctl binary has no libc split.")
	return cmd
}
