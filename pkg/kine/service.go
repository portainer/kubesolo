package kine

import (
	"context"
	"sync"
)

// service is the service for the kine server
type service struct {
	wg          sync.WaitGroup
	databaseDir string
	kineReady   chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	dbWALRepair bool
}

// NewService creates a new kine service
func NewService(ctx context.Context, cancel context.CancelFunc, databaseDir string, kineReady chan struct{}, dbWALRepair bool) *service {
	return &service{
		databaseDir: databaseDir,
		kineReady:   kineReady,
		ctx:         ctx,
		cancel:      cancel,
		dbWALRepair: dbWALRepair,
	}
}
