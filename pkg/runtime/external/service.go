// Package external attaches KubeSolo to a container runtime the host manages,
// instead of starting the containerd KubeSolo embeds. It is selected by
// --container-runtime-endpoint.
package external

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	internalapi "k8s.io/cri-api/pkg/apis"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	criclient "k8s.io/cri-client/pkg"
)

// component is the log component name for this service
const component = "container-runtime"

// service is the service for a container runtime managed by the host
type service struct {
	ctx          context.Context
	cancel       context.CancelFunc
	runtimeReady chan<- struct{}
	// embedded carries the resolved endpoint in, and the cgroup driver
	// discovered from the runtime back out for the kubelet to use.
	embedded *types.Embedded
}

// NewService creates a new external container runtime service
func NewService(ctx context.Context, cancel context.CancelFunc, runtimeReady chan<- struct{}, embedded *types.Embedded) *service {
	return &service{
		ctx:          ctx,
		cancel:       cancel,
		runtimeReady: runtimeReady,
		embedded:     embedded,
	}
}

// Run attaches to the host-managed container runtime in the following order:
// 1. it connects to the CRI endpoint, retrying until the runtime answers
// 2. it logs the runtime name and version, and warns about any condition the runtime reports as not ready
// 3. it records the runtime's cgroup driver for the kubelet
// 4. it signals readiness and waits for shutdown
//
// KubeSolo does not own this runtime, so there is no process to supervise and
// nothing to terminate: no containerd config is written, no socket is symlinked
// and no images are imported. Everything beyond the CRI socket belongs to the host.
func (s *service) Run() error {
	log.Info().Str("component", component).Str("endpoint", s.embedded.RuntimeEndpoint).Msg("attaching to host-managed container runtime...")

	client, err := s.connect()
	if err != nil {
		log.Error().Str("component", component).Msgf("failed to connect to container runtime: %v...", err)
		s.cancel()
		return err
	}
	defer func() { _ = client.Close() }()

	if err := s.inspect(client); err != nil {
		log.Error().Str("component", component).Msgf("failed to inspect container runtime: %v...", err)
		s.cancel()
		return err
	}

	close(s.runtimeReady)

	<-s.ctx.Done()
	log.Debug().Str("component", component).Msg("context cancelled, detaching from container runtime")

	return nil
}

// connect dials the CRI endpoint until the runtime answers. NewRemoteRuntimeService
// issues a CRI v1 Version call as part of connecting, so a successful return means
// the runtime is up and speaks the API — a socket that does not exist yet and a
// runtime that is still starting both fail here and are retried. The retry budget
// is the same --startup-timeout every other component uses.
func (s *service) connect() (internalapi.RuntimeService, error) {
	var lastErr error
	for attempt := range types.DefaultRetryCount {
		client, err := criclient.NewRemoteRuntimeService(s.embedded.RuntimeEndpoint, types.DefaultContextTimeout, nil, nil)
		if err == nil {
			return client, nil
		}

		lastErr = err
		s.logWaiting(err, attempt)

		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case <-time.After(types.DefaultComponentSleep):
		}
	}

	return nil, fmt.Errorf("container runtime at %s did not respond: %v (check --container-runtime-endpoint and that the runtime is running)", s.embedded.RuntimeEndpoint, lastErr)
}

// logWaiting reports why the runtime is not usable yet, once per attempt. It is a
// warning rather than a debug line because the host, not kubesolo, has to fix it:
// staying quiet through the whole --startup-timeout budget makes a runtime that was
// never started look like kubesolo hanging.
//
// A missing socket is called out separately from a socket that is present but not
// answering, because the two need different things from the operator.
func (s *service) logWaiting(err error, attempt int) {
	if _, statErr := os.Stat(s.embedded.RuntimeSocketPath); statErr != nil {
		log.Warn().Str("component", component).
			Msgf("waiting for the container runtime socket %s to appear, the host has to start its container runtime (attempt %d/%d)...", s.embedded.RuntimeSocketPath, attempt+1, types.DefaultRetryCount)
		return
	}

	log.Warn().Str("component", component).
		Msgf("container runtime at %s is not answering yet: %v (attempt %d/%d)...", s.embedded.RuntimeEndpoint, err, attempt+1, types.DefaultRetryCount)
}

// inspect reads what KubeSolo needs to know about a runtime it does not own
func (s *service) inspect(client internalapi.RuntimeService) error {
	ctx, cancel := context.WithTimeout(s.ctx, types.DefaultContextTimeout)
	defer cancel()

	version, err := client.Version(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to get container runtime version: %v", err)
	}

	log.Info().Str("component", component).
		Str("runtime", version.GetRuntimeName()).
		Str("version", version.GetRuntimeVersion()).
		Str("api", version.GetRuntimeApiVersion()).
		Msg("attached to host-managed container runtime")

	s.warnUnreadyConditions(ctx, client)
	s.resolveCgroupDriver(ctx, client)

	return nil
}

// warnUnreadyConditions surfaces the runtime's own view of its readiness. A
// NetworkReady=false condition means the runtime has not loaded a usable CNI
// config — the most common failure when the host owns the CNI plugin binaries —
// and without this the only symptom is pods stuck in ContainerCreating.
//
// These are warnings, not errors: the conditions are commonly false for a moment
// while the runtime settles, and the kubelet waits for runtime readiness itself.
func (s *service) warnUnreadyConditions(ctx context.Context, client internalapi.RuntimeService) {
	status, err := client.Status(ctx, false)
	if err != nil {
		log.Warn().Str("component", component).Msgf("failed to get container runtime status: %v", err)
		return
	}

	for _, condition := range status.GetStatus().GetConditions() {
		if condition.GetStatus() {
			continue
		}

		log.Warn().Str("component", component).
			Str("condition", condition.GetType()).
			Str("reason", condition.GetReason()).
			Msgf("container runtime reports %s is not ready: %s (the host owns this runtime, its CNI plugins and its sandbox image)", condition.GetType(), condition.GetMessage())
	}
}

// resolveCgroupDriver asks the runtime which cgroup driver it uses so the kubelet
// can be configured to match. A mismatch lets pods start and then evicts them with
// errors that never name the cause.
//
// CgroupDriver_SYSTEMD is the protobuf zero value, so an error or a response
// carrying no linux configuration must not be read as "systemd": the field is left
// empty and the kubelet falls back to detecting the driver from the host.
func (s *service) resolveCgroupDriver(ctx context.Context, client internalapi.RuntimeService) {
	config, err := client.RuntimeConfig(ctx)
	if err != nil {
		log.Debug().Str("component", component).Msgf("container runtime does not report its cgroup driver: %v", err)
		return
	}

	if config.GetLinux() == nil {
		log.Debug().Str("component", component).Msg("container runtime reported no linux configuration, so no cgroup driver")
		return
	}

	switch config.GetLinux().GetCgroupDriver() {
	case runtimeapi.CgroupDriver_SYSTEMD:
		s.embedded.RuntimeCgroupDriver = "systemd"
	case runtimeapi.CgroupDriver_CGROUPFS:
		s.embedded.RuntimeCgroupDriver = "cgroupfs"
	}

	log.Info().Str("component", component).Str("driver", s.embedded.RuntimeCgroupDriver).Msg("using the container runtime's cgroup driver")
}
