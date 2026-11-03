package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func serveLatest(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := latestReleaseURL
	latestReleaseURL = srv.URL + "/portainer/kubesolo/releases/latest"
	t.Cleanup(func() { latestReleaseURL = old })
}

func TestLatestRelease(t *testing.T) {
	serveLatest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %s, want HEAD", r.Method)
		}
		http.Redirect(w, r, "https://github.com/portainer/kubesolo/releases/tag/v1.2.3", http.StatusFound)
	})
	v, err := LatestRelease(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "v1.2.3" {
		t.Errorf("LatestRelease = %s, want v1.2.3", v)
	}
}

func TestLatestReleaseFailures(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"no redirect": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
		"not a release tag": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://github.com/portainer/kubesolo/releases", http.StatusFound)
		},
		"slow": func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		},
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			serveLatest(t, h)
			if v, err := LatestRelease(context.Background(), 100*time.Millisecond); err == nil {
				t.Errorf("LatestRelease = %s, want an error", v)
			}
		})
	}
}
