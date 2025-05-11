package kubeproxy

import "context"

type service struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	kubeproxyReady      chan<- struct{}
	adminKubeconfigFile string
}

func NewService(ctx context.Context, cancel context.CancelFunc, kubeproxyReady chan<- struct{}, adminKubeconfigFile string) *service {
	return &service{
		ctx:                 ctx,
		cancel:              cancel,
		kubeproxyReady:      kubeproxyReady,
		adminKubeconfigFile: adminKubeconfigFile,
	}
}
