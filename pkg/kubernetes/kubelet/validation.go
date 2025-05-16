package kubelet

import (
	"fmt"
	"os"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/rs/zerolog/log"
)

// validation validates the kubelet in the following order:
// 1. it ensures the kubelet directory exists
// 2. it checks if the containerd socket path exists
// 3. it returns an error if it fails
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
