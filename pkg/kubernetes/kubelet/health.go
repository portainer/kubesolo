package kubelet

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/rs/zerolog/log"
)

func (s *service) validation() error {
	if err := filesystem.EnsureDirectoryExists(s.kubeletDir); err != nil {
		return fmt.Errorf("failed to create kubelet directory: %v", err)
	}

	if _, err := os.Stat(s.containerdSockFile); os.IsNotExist(err) {
		log.Error().Str("component", "kubelet").Msgf("containerd socket path %s does not exist", s.containerdSockFile)
		return fmt.Errorf("containerd socket path %s does not exist", s.containerdSockFile)
	}

	return nil
}

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
