package kubelet

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/network"
)

// checkKubeletHealth checks the health of the kubelet
func (s *service) checkKubeletHealth() error {
	cert, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
	if err != nil {
		return fmt.Errorf("failed to load client certificates: %v", err)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{cert},
			},
		},
	}

	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, "https://127.0.0.1:10250/healthz", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %v", err)
	}

	return network.IsComponentHealthy(client, req, "kubelet")
}
