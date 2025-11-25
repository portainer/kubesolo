package containerd

import (
	"context"
	"sync"

	"github.com/portainer/kubesolo/types"
)

// service is the service for the containerd
type service struct {
	wg                            sync.WaitGroup
	ctx                           context.Context
	cancel                        context.CancelFunc
	containerdReady               chan<- struct{}
	containerdBinaryFile          string
	containerdImagesDir           string
	containerdConfigFile          string
	containerdRootDir             string
	containerdStateDir            string
	containerdSocketFile          string
	containerdCNIPluginsDir       string
	runcBinaryFile                string
	containerdShimBinaryFile      string
	portainerAgentImageFile       string
	corednsImageFile              string
	sandboxImageFile              string
	localPathProvisionerImageFile string
	isPortainerEdge               bool
}

// NewService creates a new containerd service
func NewService(ctx context.Context, cancel context.CancelFunc, containerdReady chan<- struct{}, embedded *types.Embedded) *service {
	return &service{
		ctx:                           ctx,
		cancel:                        cancel,
		containerdReady:               containerdReady,
		containerdBinaryFile:          embedded.ContainerdBinaryFile,
		containerdImagesDir:           embedded.ContainerdImagesDir,
		containerdConfigFile:          embedded.ContainerdConfigFile,
		containerdRootDir:             embedded.ContainerdRootDir,
		containerdStateDir:            embedded.ContainerdStateDir,
		containerdSocketFile:          embedded.ContainerdSocketFile,
		containerdCNIPluginsDir:       embedded.ContainerdCNIPluginsDir,
		runcBinaryFile:                embedded.RuncBinaryFile,
		containerdShimBinaryFile:      embedded.ContainerdShimBinaryFile,
		portainerAgentImageFile:       embedded.PortainerAgentImageFile,
		corednsImageFile:              embedded.CorednsImageFile,
		sandboxImageFile:              embedded.SandboxImageFile,
		localPathProvisionerImageFile: embedded.LocalPathProvisionerImageFile,
		isPortainerEdge:               embedded.IsPortainerEdge,
	}
}
