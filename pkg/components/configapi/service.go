// Package configapi implements the optional KubeSolo configuration API.
//
// It serves the configuration document over a unix socket: read it, replace it,
// patch it, reset it, or check a candidate without saving. The socket's file
// permissions are the authorisation model — there is no token and no TLS,
// because there is no network listener to protect. On an edge device, a
// mutating endpoint that is not reachable over the network is worth more than
// one that is reachable and authenticated.
//
// The API manages desired state, not live state. Almost every setting is read
// once during bootstrap and baked into types.Embedded, which each service is
// constructed from by value, so a change takes effect when KubeSolo restarts.
// Responses say so rather than implying otherwise.
package configapi

import (
	"context"
	"sync"

	"github.com/portainer/kubesolo/internal/config"
)

// Options is everything the service needs.
//
// Unlike the metrics service it does not take types.Embedded: it reads and
// writes the configuration file, and needs neither the PKI nor the containerd
// layout to do it.
type Options struct {
	// SocketPath is where the unix socket is created.
	SocketPath string

	// ConfigPath is the configuration file served and updated.
	ConfigPath string

	// Host describes the machine, for validating candidate configurations.
	Host config.Host
}

// Service is the configuration API. It follows the same NewService/Run + readyCh
// shape as the other KubeSolo components.
type Service struct {
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	readyCh chan struct{}
	opts    Options

	// writeMu serialises writes within this process, so two concurrent callers
	// cannot both read the document and both write it, losing one change. A
	// writer outside this process is caught by the If-Match precondition instead.
	writeMu sync.Mutex
}

// NewService creates the configuration API service.
//
// It does not block any other component on its own readiness: readyCh exists so
// that main can join the goroutine on shutdown. The API failing must never stop
// KubeSolo from running.
func NewService(ctx context.Context, cancel context.CancelFunc, readyCh chan struct{}, opts Options) *Service {
	return &Service{
		ctx:     ctx,
		cancel:  cancel,
		readyCh: readyCh,
		opts:    opts,
	}
}
