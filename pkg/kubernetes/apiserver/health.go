package apiserver

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/network"
)

// checkAPIServerReadiness checks if the API server is ready to serve requests
// by verifying /healthz, /readyz, and /livez endpoints
func (s *service) checkAPIServerReadiness() error {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	endpoints := []string{"/healthz", "/readyz", "/livez"}
	for _, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, "https://127.0.0.1:6443"+endpoint, nil)
		if err != nil {
			return fmt.Errorf("failed to create %s check request: %v", endpoint, err)
		}

		if err := network.IsComponentHealthy(client, req, fmt.Sprintf("apiserver%s", endpoint)); err != nil {
			return fmt.Errorf("apiserver %s check failed: %v", endpoint, err)
		}
	}

	return nil
}
