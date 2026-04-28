package kubeproxy

import (
	"context"
	"sync"
)

// service is the service for the kube proxy
type service struct {
	wg                  sync.WaitGroup
	ctx                 context.Context
	cancel              context.CancelFunc
	kubeproxyReady      chan<- struct{}
	adminKubeconfigFile string
	fullMode            bool
}

// NewService creates a new kube proxy service
func NewService(ctx context.Context, cancel context.CancelFunc, kubeproxyReady chan<- struct{}, adminKubeconfigFile string, fullMode bool) *service {
	return &service{
		ctx:                 ctx,
		cancel:              cancel,
		kubeproxyReady:      kubeproxyReady,
		adminKubeconfigFile: adminKubeconfigFile,
		fullMode:            fullMode,
	}
}
