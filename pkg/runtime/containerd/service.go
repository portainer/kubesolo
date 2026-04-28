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
	containerdRegistryConfigDir   string
	crunBinaryFile                string
	containerdShimBinaryFile      string
	portainerAgentImageFile       string
	corednsImageFile              string
	sandboxImageFile              string
	localPathProvisionerImageFile string
	isPortainerEdge               bool
	fullMode                      bool
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
		containerdRegistryConfigDir:   embedded.ContainerdRegistryConfigDir,
		crunBinaryFile:                embedded.CrunBinaryFile,
		containerdShimBinaryFile:      embedded.ContainerdShimBinaryFile,
		portainerAgentImageFile:       embedded.PortainerAgentImageFile,
		corednsImageFile:              embedded.CorednsImageFile,
		sandboxImageFile:              embedded.SandboxImageFile,
		localPathProvisionerImageFile: embedded.LocalPathProvisionerImageFile,
		isPortainerEdge:               embedded.IsPortainerEdge,
		fullMode:                      embedded.FullMode,
	}
}
