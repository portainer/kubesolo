package containerd

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd/v2/client"
	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// validation checks if the containerd state and root directories exist
// it will return an error if they do not exist
func (s *service) validation() error {
	if err := filesystem.EnsureDirectoryExists(s.containerdStateDir); err != nil {
		log.Error().Str("component", "containerd").Msgf("failed to create containerd state directory: %v", err)
		return err
	}

	if err := filesystem.EnsureDirectoryExists(s.containerdRootDir); err != nil {
		log.Error().Str("component", "containerd").Msgf("failed to create containerd root directory: %v", err)
		return err
	}

	return nil
}

// checkContainerdHealth checks if containerd is healthy by connecting to it and getting the version
// it will return an error if it is not healthy
func (s *service) checkContainerdHealth(ctx context.Context, client *client.Client) error {
	for range types.DefaultRetryCount {
		_, err := client.Version(ctx)
		if err != nil {
			log.Warn().Str("component", "containerd").Msgf("containerd health check failed: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		return nil
	}

	return fmt.Errorf("containerd health check failed after multiple attempts")
}
