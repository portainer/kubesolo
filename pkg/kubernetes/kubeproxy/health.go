package kubeproxy

import (
	"fmt"
	"net/http"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/network"
)

func (s *service) checkKubeProxyHealth() error {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, "http://127.0.0.1:10256/healthz", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %v", err)
	}

	return network.IsComponentHealthy(client, req, "kubeproxy")
}
