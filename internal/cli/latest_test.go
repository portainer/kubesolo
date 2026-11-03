package cli

import (
	"context"
	"errors"
	"testing"
	"time"

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
