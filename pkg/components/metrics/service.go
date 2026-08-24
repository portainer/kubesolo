// Package metrics implements the optional kubesolo Prometheus metrics endpoint.
//
// It exposes a small /metrics HTTP server that surfaces KubeSolo-native gauges
// describing the health of each control plane component (apiserver, kine,
// controller-manager, kubelet, kube-proxy, runtime, coredns, webhook),
// plus build info, kine DB size, and process uptime.
//
// Component-internal Prometheus metrics are out of scope — this endpoint is
// intentionally small and focused on operator-facing health monitoring.
package metrics

import (
	"context"
	"sync"

	"github.com/portainer/kubesolo/types"
	"github.com/prometheus/client_golang/prometheus"
)

// BuildInfo carries version metadata stamped by the Makefile via -ldflags.
// It is used to populate the kubesolo_build_info gauge.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// Service is the kubesolo metrics endpoint service. It follows the same
// NewService/Run + readyCh pattern as the other kubesolo components.
type Service struct {
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	readyCh  chan struct{}
	embedded types.Embedded
	build    BuildInfo

	// readiness channels for each component, keyed by component name.
	// Each channel is closed when the corresponding component first becomes ready.
	componentReady map[string]<-chan struct{}

	registry *prometheus.Registry
	metrics  *componentMetrics
	startAt  int64
}

// NewService creates a new metrics service.
//
// componentReady is a map of component name to readiness channel. The metrics
// service watches each channel and flips kubesolo_component_up to 1 plus
// stamps kubesolo_component_ready_timestamp_seconds when the channel closes.
//
// The service does not block any other component on its own readiness — the
// readyCh argument is only used so main can join the goroutine on shutdown.
func NewService(
	ctx context.Context,
	cancel context.CancelFunc,
	readyCh chan struct{},
	embedded types.Embedded,
	build BuildInfo,
	componentReady map[string]<-chan struct{},
) *Service {
	return &Service{
		ctx:            ctx,
		cancel:         cancel,
		readyCh:        readyCh,
		embedded:       embedded,
		build:          build,
		componentReady: componentReady,
	}
}
