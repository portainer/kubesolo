package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/portainer/kubesolo/internal/upgrade"
)

// latestReleaseTimeout bounds the lookup: it only feeds a notice, so a host
// with no route to GitHub must not be held up by it.
const latestReleaseTimeout = 3 * time.Second

// latestRelease is upgrade.LatestRelease, replaced in tests.
var latestRelease = upgrade.LatestRelease

// newerReleaseNotice warns when a newer KubeSolo release exists than the one
// this kubesoloctl installs by default. The default is fixed when kubesoloctl
// is built, so an old kubesoloctl keeps installing an old release, while the
// shell installer is fetched fresh and installs the latest. It stays the
// default: an older kubesoloctl writes configuration only for the releases it
// knows.
//
// Only a version left at its default is checked. Nothing is said when the
// lookup fails, or when releases come from a mirror, which suggests the host
// has no route to GitHub anyway.
func newerReleaseNotice(p *ui.Printer, cmd *cobra.Command, version string) {
	if cmd.Flags().Changed("version") || os.Getenv("KUBESOLO_VERSION") != "" ||
		os.Getenv("KUBESOLO_RELEASE_BASE_URL") != "" {
		return
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if latest := newerRelease(ctx, version); latest != "" {
		p.Warn(fmt.Sprintf("KubeSolo %s is available; this kubesoloctl installs %s by default. "+
			"Update kubesoloctl (https://github.com/portainer/kubesolo/releases/latest) or pass --version=%s",
			latest, version, latest))
	}
}

// versionFromInput reports whether an offline archive or a custom image
// decides the version install installs, rather than the default.
func versionFromInput(cfg *config.Config, containerMode bool) bool {
	return cfg.OfflineInstall != "" || (containerMode && cfg.ContainerImage != "")
}

// newerRelease returns the latest release when it is newer than version, and
// "" otherwise or when the lookup fails.
func newerRelease(ctx context.Context, version string) string {
	current, err := upgrade.ParseVersion(version)
	if err != nil {
		return ""
	}
	latest, err := latestRelease(ctx, latestReleaseTimeout)
	if err != nil || latest.Compare(current) <= 0 {
		return ""
	}
	return latest.String()
}
