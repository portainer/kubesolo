package containerd

import (
	"context"

	"github.com/portainer/kubesolo/types"
)

type service struct {
	ctx                     context.Context
	cancel                  context.CancelFunc
	containerdReady         chan<- struct{}
	containerdBinaryFile    string
	containerdImagesDir     string
	containerdConfigFile    string
	containerdRootDir       string
	containerdStateDir      string
	containerdSocketFile    string
	runcBinaryFile          string
	portainerAgentImageFile string
	corednsImageFile        string
	isPortainerEdge         bool
}

func NewService(ctx context.Context, cancel context.CancelFunc, containerdReady chan<- struct{}, embedded *types.Embedded) *service {
	return &service{
		ctx:                     ctx,
		cancel:                  cancel,
		containerdReady:         containerdReady,
		containerdBinaryFile:    embedded.ContainerdBinaryFile,
		containerdImagesDir:     embedded.ContainerdImagesDir,
		containerdConfigFile:    embedded.ContainerdConfigFile,
		containerdRootDir:       embedded.ContainerdRootDir,
		containerdStateDir:      embedded.ContainerdStateDir,
		containerdSocketFile:    embedded.ContainerdSocketFile,
		runcBinaryFile:          embedded.RuncBinaryFile,
		portainerAgentImageFile: embedded.PortainerAgentImageFile,
		corednsImageFile:        embedded.CorednsImageFile,
		isPortainerEdge:         embedded.IsPortainerEdge,
	}
}
