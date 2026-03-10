package kine

import (
	"context"
	"sync"
)

// service is the service for the kine server
type service struct {
	wg          sync.WaitGroup
	databaseDir string
	inMemory    bool
	kineReady   chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewService creates a new kine service.
// When inMemory is true, kine uses an experimental in-memory store instead of SQLite.
func NewService(ctx context.Context, cancel context.CancelFunc, databaseDir string, kineReady chan struct{}, inMemory bool) *service {
	return &service{
		databaseDir: databaseDir,
		inMemory:    inMemory,
		kineReady:   kineReady,
		ctx:         ctx,
		cancel:      cancel,
	}
}
