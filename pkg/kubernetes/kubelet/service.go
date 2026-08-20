package kubelet

import (
	"context"
	"sync"

	"github.com/portainer/kubesolo/internal/system"
	"github.com/portainer/kubesolo/types"
)

// service is the service for the kubelet
type service struct {
	wg                    sync.WaitGroup
	ctx                   context.Context
	cancel                context.CancelFunc
	kubeletReady          chan<- struct{}
	kubeletDir            string
	kubeletConfigDir      string
	kubeletConfigFile     string
	kubeletKubeConfigFile string
	runtimeEndpoint       string
	runtimeSocketPath     string
	runtimeCgroupDriver   string
	caFile                string
	certFile              string
	keyFile               string
	nodeName              string
	nodeIP                string
	kubeletCertPath       string
	adminKubeconfig       string
	containerMode         bool
	disableIPv6           bool
	cpuManager            types.CPUManagerConfig
}

// NewService creates a new kubelet service.
//
// RuntimeCgroupDriver is read here rather than at Run time on purpose: an external
// container runtime discovers it and writes it before closing its readiness channel,
// and kubesolo starts the kubelet only after that channel is closed, so the value is
// already settled and the channel provides the synchronisation.
func NewService(ctx context.Context, cancel context.CancelFunc, kubeletReady chan<- struct{}, embedded *types.Embedded) *service {
	return &service{
		ctx:                   ctx,
		cancel:                cancel,
		kubeletReady:          kubeletReady,
		kubeletDir:            embedded.KubeletDir,
		kubeletConfigDir:      embedded.KubeletConfigDir,
		nodeIP:                embedded.NodeIP,
		kubeletCertPath:       embedded.PKIAdminDir,
		kubeletConfigFile:     embedded.KubeletConfigFile,
		kubeletKubeConfigFile: embedded.KubeletKubeConfigFile,
		runtimeEndpoint:       embedded.RuntimeEndpoint,
		runtimeSocketPath:     embedded.RuntimeSocketPath,
		runtimeCgroupDriver:   embedded.RuntimeCgroupDriver,
		caFile:                embedded.KubeletCerts.CACert,
		certFile:              embedded.KubeletCerts.Cert,
		keyFile:               embedded.KubeletCerts.Key,
		nodeName:              system.GetHostname(),
		adminKubeconfig:       embedded.AdminKubeconfigFile,
		containerMode:         embedded.ContainerMode,
		disableIPv6:           embedded.DisableIPv6,
		cpuManager:            embedded.CPUManager,
	}
}
