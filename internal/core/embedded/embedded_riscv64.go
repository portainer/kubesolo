//go:build linux && riscv64

package embedded

import (
	_ "embed"
	"fmt"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

//go:embed bin/containerd/bin/containerd-shim-runc-v2.zst
var containerdShimBinary []byte

//go:embed bin/crun.zst
var crunBinary []byte

//go:embed bin/cni/bridge.zst
var cniPluginBridge []byte

//go:embed bin/cni/host-local.zst
var cniPluginHostLocal []byte

//go:embed bin/cni/portmap.zst
var cniPluginPortmap []byte

//go:embed bin/cni/loopback.zst
var cniPluginLoopback []byte

//go:embed bin/images/coredns.tar.gz
var corednsImageFile []byte

//go:embed bin/images/pause.tar.gz
var sandboxImageFile []byte

// EnsureEmbeddedDependencies ensures all required components are available
// it loads the containerd components, cni plugins, cni config, images, and kernel modules
// before the kubesolo application starts
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
