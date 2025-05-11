package controller

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/network"
)

// checkControllerManagerHealth checks if the controller manager is working properly
func (s *service) checkControllerManagerHealth() error {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, "https://127.0.0.1:10257/healthz", nil)
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
	}, req, "controller-manager")
}
