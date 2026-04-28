package kubelet

import (
	"context"
	"sync"

	client "github.com/containerd/containerd/v2/client"
	"github.com/portainer/kubesolo/internal/system"
	"github.com/portainer/kubesolo/types"
)

// service is the service for the kubelet
type service struct {
	wg                    sync.WaitGroup
	client                *client.Client
	ctx                   context.Context
	cancel                context.CancelFunc
	kubeletReady          chan<- struct{}
	kubeletDir            string
	kubeletConfigDir      string
	kubeletConfigFile     string
	kubeletKubeConfigFile string
	containerdSockFile    string
	caFile                string
	certFile              string
	keyFile               string
	nodeName              string
	nodeIP                string
	kubeletCertPath       string
	adminKubeconfig       string
	fullMode              bool
}

// NewService creates a new kubelet service
func NewService(ctx context.Context, cancel context.CancelFunc, kubeletReady chan<- struct{}, embedded *types.Embedded) *service {
	return &service{
		ctx:                   ctx,
		cancel:                cancel,
		client:                nil,
		kubeletReady:          kubeletReady,
		kubeletDir:            embedded.KubeletDir,
		kubeletConfigDir:      embedded.KubeletConfigDir,
		nodeIP:                embedded.NodeIP,
		kubeletCertPath:       embedded.PKIAdminDir,
		kubeletConfigFile:     embedded.KubeletConfigFile,
		kubeletKubeConfigFile: embedded.KubeletKubeConfigFile,
		containerdSockFile:    embedded.ContainerdSocketFile,
		caFile:                embedded.KubeletCerts.CACert,
		certFile:              embedded.KubeletCerts.Cert,
		keyFile:               embedded.KubeletCerts.Key,
		nodeName:              system.GetHostname(),
		adminKubeconfig:       embedded.AdminKubeconfigFile,
		fullMode:              embedded.FullMode,
	}
}
