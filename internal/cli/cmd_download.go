package cli

import (
	"fmt"
	"runtime"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/detect"
	"github.com/portainer/kubesolo/internal/cli/download"
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
  kubesoloctl download --version=v1.1.5 --path=./offline-bundle
  kubesoloctl download --version=v1.1.5 --path=./offline-bundle --arch=arm64
  kubesoloctl download --version=v1.1.5 --path=./offline-bundle --arch=amd64-musl`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				dir = "."
			}
			// KubeSolo has no macOS binaries — the target arch must be specified
			// explicitly when downloading from a Mac for deployment to a Linux device.
			if targetArch == "" && runtime.GOOS == "darwin" {
				return fmt.Errorf("on macOS, KubeSolo has no native binaries — specify the target Linux arch with --arch (e.g. --arch=amd64 or --arch=arm64)")
			}
			var info *detect.SystemInfo
			var err error
			if targetArch != "" {
				info, err = detect.ForTarget(targetArch)
			} else {
				info, err = detect.Detect()
			}
			if err != nil {
				return err
			}
			return download.DownloadBundle(dir, info.ArchiveName(cfg.Version), info.InstallerName(), cfg.Version)
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
