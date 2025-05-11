package embedded

import (
	"embed"
	"fmt"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

//go:embed bin/containerd/bin/containerd
var containerdBinary []byte

//go:embed bin/containerd/bin/containerd-shim-runc-v2
var containerdShimBinary []byte

//go:embed bin/runc
var runcBinary []byte

//go:embed bin/cni/*
var cniPluginsFS embed.FS

//go:embed bin/images/coredns.tar.gz
var corednsImageFile []byte

//go:embed bin/images/portainer-agent.tar.gz
var portainerAgentImageFile []byte

// EnsureEmbeddedDependencies ensures all required components are available
func EnsureEmbeddedDependencies(embedded types.Embedded) error {
	if err := loadContainerdComponents(embedded); err != nil {
		return fmt.Errorf("failed to load containerd: %v", err)
	}

	if err := loadCNIPlugins(embedded.ContainerdCNIDir, embedded.ContainerdCNIPluginsDir); err != nil {
		return fmt.Errorf("failed to load cni plugins: %v", err)
	}

	if err := loadCNIConfig(embedded.ContainerdCNIConfigDir, embedded.ContainerdCNIConfigFile); err != nil {
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
