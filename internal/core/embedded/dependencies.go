package embedded

import (
	"fmt"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// EnsureEmbeddedDependencies ensures all required components are available
// it loads the containerd components, cni plugins, cni config, images, and kernel modules
// before the kubesolo application starts
//
// When kubesolo attaches to a container runtime the host manages there is nothing
// to extract and no images to stage, so only the kernel modules and the CNI config
// are prepared — see ensureHostDependencies.
func EnsureEmbeddedDependencies(embedded types.Embedded) error {
	if embedded.RuntimeExternal {
		return ensureHostDependencies(embedded)
	}

	if err := loadContainerdComponents(embedded); err != nil {
		return fmt.Errorf("failed to load containerd: %v", err)
	}

	if err := loadCNIPlugins(embedded.ContainerdCNIDir, embedded.ContainerdCNIPluginsDir); err != nil {
		return fmt.Errorf("failed to load cni plugins: %v", err)
	}

	if err := loadCNIConfig(embedded.ContainerdCNIConfigDir, embedded.ContainerdCNIConfigFile, embedded.MTU); err != nil {
		return fmt.Errorf("failed to load cni config: %v", err)
	}

	if err := loadImages(embedded.ContainerdImagesDir); err != nil {
		return fmt.Errorf("failed to load images: %v", err)
	}

	if err := loadKernelModules(); err != nil {
		log.Warn().Str("component", "embedded").Msgf("failed to load kernel modules: %v", err)
	}

	return nil
}
