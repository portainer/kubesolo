package kubelet

import (
	"context"

	client "github.com/containerd/containerd/v2/client"
	"github.com/portainer/kubesolo/internal/system"
	"github.com/portainer/kubesolo/types"
)

// service is the service for the kubelet
type service struct {
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
	kubeletCertPath       string
	adminKubeconfig       string
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
		kubeletCertPath:       embedded.PKIAdminDir,
		kubeletConfigFile:     embedded.KubeletConfigFile,
		kubeletKubeConfigFile: embedded.KubeletKubeConfigFile,
		containerdSockFile:    embedded.ContainerdSocketFile,
		caFile:                embedded.KubeletCerts.CACert,
		certFile:              embedded.KubeletCerts.Cert,
		keyFile:               embedded.KubeletCerts.Key,
		nodeName:              system.GetHostname(),
		adminKubeconfig:       embedded.AdminKubeconfigFile,
	}
}
