package upgrade

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"time"
)

// latestReleaseURL redirects to the tag page of the newest release, which
// GitHub keeps pointing at the release marked latest: never a pre-release or a
// draft. A redirect is followed rather than the releases API asked, because the
// API allows 60 unauthenticated requests an hour per address, which a fleet
// behind one NAT spends quickly.
var latestReleaseURL = "https://github.com/portainer/kubesolo/releases/latest"

// LatestRelease returns the newest KubeSolo release, as its tag. It gives up
// after timeout, so a host with no route to GitHub is held up only briefly.
func LatestRelease(ctx context.Context, timeout time.Duration) (Version, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestReleaseURL, nil)
	if err != nil {
		return Version{}, err
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return Version{}, fmt.Errorf("HEAD %s: %w", latestReleaseURL, err)
	}
	_ = resp.Body.Close()
	loc, err := resp.Location()
	if err != nil {
		if errors.Is(err, http.ErrNoLocation) {
			return Version{}, fmt.Errorf("HEAD %s returned HTTP %d without a redirect", latestReleaseURL, resp.StatusCode)
		}
		return Version{}, err
	}
	// .../releases/tag/v1.3.0
	return ParseVersion(path.Base(loc.Path))
}
