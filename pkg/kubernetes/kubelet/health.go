package kubelet

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"
)

const kubeletHealthTimeout = 2 * time.Minute

// checkKubeletHealth polls the kubelet health endpoint until it responds 200
// or the timeout elapses. A 2-minute budget accommodates constrained devices
// where in-process CoreDNS informers compete for CPU during kubelet startup.
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

	deadline := time.Now().Add(kubeletHealthTimeout)
	for {
		req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, "https://127.0.0.1:10250/healthz", nil)
		if err != nil {
			return fmt.Errorf("failed to create health check request: %v", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("kubelet did not become healthy within %s", kubeletHealthTimeout)
		}

		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
