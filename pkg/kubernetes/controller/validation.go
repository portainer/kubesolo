package controller

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/internal/runtime/network"
)

// validation validates the controller manager in the following order:
// 1. it ensures the controller directory exists
// 2. it creates the health check request to the API server
// 3. it checks if the API server is healthy
// 4. it returns an error if it fails
func (s *service) validation() error {
	if err := filesystem.EnsureDirectoryExists(s.controllerDir); err != nil {
		return fmt.Errorf("failed to create controller directory: %v", err)
	}

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
	}, req, "controller")
}
