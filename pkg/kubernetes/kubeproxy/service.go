package kubeproxy

import "context"

// service is the service for the kube proxy
type service struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	kubeproxyReady      chan<- struct{}
	adminKubeconfigFile string
}

// NewService creates a new kube proxy service
func NewService(ctx context.Context, cancel context.CancelFunc, kubeproxyReady chan<- struct{}, adminKubeconfigFile string) *service {
	return &service{
		ctx:                 ctx,
		cancel:              cancel,
		kubeproxyReady:      kubeproxyReady,
		adminKubeconfigFile: adminKubeconfigFile,
	}
}
