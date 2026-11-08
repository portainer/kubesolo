package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/portainer/kubesolo/internal/cli/config"
	"github.com/portainer/kubesolo/internal/cli/ui"
	"github.com/portainer/kubesolo/internal/upgrade"
)

func TestNewerRelease(t *testing.T) {
	tests := []struct {
		name    string
		version string
		latest  string
		err     error
		want    string
	}{
		{name: "newer release", version: "v1.2.1", latest: "v1.2.3", want: "v1.2.3"},
		{name: "same release", version: "v1.2.3", latest: "v1.2.3"},
		{name: "default ahead of latest", version: "v1.2.4", latest: "v1.2.3"},
		{name: "pre-release default", version: "v1.3.0-rc.1", latest: "v1.3.0", want: "v1.3.0"},
		{name: "unparseable default", version: "dev", latest: "v1.2.3"},
		{name: "lookup fails", version: "v1.2.1", err: errors.New("no route to host")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			old := latestRelease
			t.Cleanup(func() { latestRelease = old })
			latestRelease = func(context.Context, time.Duration) (upgrade.Version, error) {
				if tc.err != nil {
					return upgrade.Version{}, tc.err
				}
				return upgrade.ParseVersion(tc.latest)
			}
			if got := newerRelease(context.Background(), tc.version); got != tc.want {
				t.Errorf("newerRelease(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}

// stubLatestRelease replaces the lookup with one that counts its calls.
func stubLatestRelease(t *testing.T) *int {
	t.Helper()
	calls := 0
	old := latestRelease
	t.Cleanup(func() { latestRelease = old })
	latestRelease = func(context.Context, time.Duration) (upgrade.Version, error) {
		calls++
		return upgrade.ParseVersion("v9.9.9")
	}
	return &calls
}

// The lookup is made only for a version left at its default, and never when
// releases come from a mirror.
func TestNewerReleaseNoticeGating(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		env    map[string]string
		lookup bool
	}{
		{name: "default version", lookup: true},
		{name: "--version", args: []string{"--version=v1.2.1"}},
		{name: "KUBESOLO_VERSION", env: map[string]string{"KUBESOLO_VERSION": "v1.2.1"}},
		{name: "mirror", env: map[string]string{"KUBESOLO_RELEASE_BASE_URL": "https://mirror.example.com/kubesolo"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KUBESOLO_VERSION", "")
			t.Setenv("KUBESOLO_RELEASE_BASE_URL", "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			calls := stubLatestRelease(t)

			var version string
			cmd := &cobra.Command{Use: "install"}
			cmd.Flags().StringVar(&version, "version", config.DefaultVersion, "")
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			newerReleaseNotice(ui.New(), cmd, version)

			if got := *calls > 0; got != tc.lookup {
				t.Errorf("looked up the latest release = %v, want %v", got, tc.lookup)
			}
		})
	}
}

// install checks for a newer release only when the default decides the
// version, not an offline archive or a custom image.
func TestVersionFromInput(t *testing.T) {
	tests := []struct {
		name          string
		cfg           config.Config
		containerMode bool
		want          bool
	}{
		{name: "host default", want: false},
		{name: "container default", containerMode: true, want: false},
		{name: "offline archive", cfg: config.Config{OfflineInstall: "/tmp/kubesolo-v1.2.1-linux-amd64.tar.gz"}, want: true},
		{name: "custom image", cfg: config.Config{ContainerImage: "registry.example.com/kubesolo:dev"}, containerMode: true, want: true},
		// The image is ignored outside container mode.
		{name: "image on a host install", cfg: config.Config{ContainerImage: "registry.example.com/kubesolo:dev"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionFromInput(&tc.cfg, tc.containerMode); got != tc.want {
				t.Errorf("versionFromInput = %v, want %v", got, tc.want)
			}
		})
	}
}
