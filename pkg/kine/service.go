package kine

import (
	"context"
)

type service struct {
	databaseDir string
	kineReady   chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewService(ctx context.Context, cancel context.CancelFunc, databaseDir string, kineReady chan struct{}) *service {
	return &service{
		databaseDir: databaseDir,
		kineReady:   kineReady,
		ctx:         ctx,
		cancel:      cancel,
	}
}
