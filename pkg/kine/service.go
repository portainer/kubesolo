package kine

import (
	"context"
)

// service is the service for the kine server
type service struct {
	databaseDir string
	kineReady   chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewService creates a new kine service
func NewService(ctx context.Context, cancel context.CancelFunc, databaseDir string, kineReady chan struct{}) *service {
	return &service{
		databaseDir: databaseDir,
		kineReady:   kineReady,
		ctx:         ctx,
		cancel:      cancel,
	}
}
