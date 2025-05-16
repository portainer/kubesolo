package apiserver

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/network"
)

// checkAPIServerHealth checks the health of the API server
func (s *service) checkAPIServerHealth() error {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, "https://127.0.0.1:6443/healthz", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %v", err)
	}

	return network.IsComponentHealthy(&http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}, req, "apiserver")
}
