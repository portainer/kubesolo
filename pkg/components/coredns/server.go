package coredns

import (
	"context"
	"fmt"
	"sync"

	"github.com/coredns/caddy"
	_ "github.com/coredns/coredns/core"
	"github.com/coredns/coredns/core/dnsserver"
	_ "github.com/coredns/coredns/core/plugin"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"k8s.io/klog/v2"
)

type service struct {
	ctx              context.Context
	cancel           context.CancelFunc
	dnsReadyCh       chan<- struct{}
	apiserverReadyCh <-chan struct{}
	embedded         types.Embedded
	instance         *caddy.Instance
	mu               sync.Mutex
}

// NewService creates a new in-process CoreDNS service.
func NewService(
	ctx context.Context,
	cancel context.CancelFunc,
	dnsReadyCh chan<- struct{},
	apiserverReadyCh chan struct{},
	embedded types.Embedded,
) *service {
	return &service{
		ctx:              ctx,
		cancel:           cancel,
		dnsReadyCh:       dnsReadyCh,
		apiserverReadyCh: apiserverReadyCh,
		embedded:         embedded,
	}
}

// Run starts CoreDNS in two stages:
//
// Forward-only: starts immediately, provides upstream DNS forwarding.
// Signals dnsReadyCh once up so controller and kubelet can start concurrently.
//
// Cluster-aware: hot-reloads after apiserver ready, enabling cluster.local
// resolution. Registers the kube-dns Service + EndpointSlice. Runs concurrently
// with controller/kubelet to avoid resource contention on constrained devices
// during the expensive initial informer cache sync.
func (s *service) Run() error {
	dnsserver.Quiet = true
	caddy.Quiet = true

	if err := s.startForwardOnlyDNS(); err != nil {
		return err
	}

	if !s.waitForAPIServer() {
		return nil
	}

	if err := s.startClusterAwareDNS(); err != nil {
		return err
	}

	if err := s.registerKubeDNS(); err != nil {
		return err
	}

	log.Info().Str("component", "coredns").Msg("coredns is ready")

	<-s.ctx.Done()
	s.stop()
	return nil
}

// startForwardOnlyDNS starts CoreDNS in forward-only mode and signals the ready channel.
func (s *service) startForwardOnlyDNS() error {
	log.Info().Str("component", "coredns").Int("port", dnsBoundPort).Msg("starting forward-only DNS...")

	instance, err := caddy.Start(newCorefileInput(forwardOnlyCorefile()))
	if err != nil {
		log.Error().Str("component", "coredns").Err(err).Msg("forward-only DNS failed to start")
		s.cancel()
		return fmt.Errorf("coredns forward-only DNS failed: %w", err)
	}
	s.instance = instance

	// Forward-only DNS is up — signal ready so controller and kubelet can start
	// while the cluster-aware reload (kubernetes plugin informers) loads concurrently.
	// Cluster DNS (cluster.local) is not available until that completes, but pods
	// aren't scheduled until kubelet + kube-proxy are up anyway.
	log.Info().Str("component", "coredns").Msg("forward-only DNS is ready")
	close(s.dnsReadyCh)
	return nil
}

// waitForAPIServer blocks until the apiserver signals ready or the context is cancelled.
// Returns false if the context was cancelled (caller should return without error).
func (s *service) waitForAPIServer() bool {
	select {
	case <-s.apiserverReadyCh:
		return true
	case <-s.ctx.Done():
		s.stop()
		return false
	}
}

// startClusterAwareDNS hot-reloads CoreDNS with the kubernetes plugin enabled.
func (s *service) startClusterAwareDNS() error {
	log.Info().Str("component", "coredns").Msg("starting cluster-aware DNS (kubernetes plugin)...")

	s.mu.Lock()
	cf := clusterAwareCorefile(
		"https://127.0.0.1:6443",
		s.embedded.CACerts.Cert,
		s.embedded.AdminCerts.Cert,
		s.embedded.AdminCerts.Key,
	)
	newInstance, err := s.instance.Restart(newCorefileInput(cf))
	s.mu.Unlock()

	// CoreDNS's kubernetes plugin calls klog.SetLogger() globally during setup,
	// redirecting all klog output (including kubelet's) to its internal logger.
	// Restore native klog output after the plugin has started.
	klog.ClearLogger()

	if err != nil {
		log.Error().Str("component", "coredns").Err(err).Msg("cluster-aware DNS failed to start")
		s.cancel()
		return fmt.Errorf("coredns cluster-aware DNS failed: %w", err)
	}
	s.instance = newInstance
	return nil
}

// registerKubeDNS cleans up any legacy pod-based CoreDNS resources, then
// creates the selectorless kube-dns Service and EndpointSlice so kube-proxy
// can create the DNAT rule for ClusterIP:53 → nodeIP:dnsBoundPort.
func (s *service) registerKubeDNS() error {
	clientset, err := kubesolokubernetes.GetKubernetesClient(s.embedded.AdminKubeconfigFile)
	if err != nil {
		log.Error().Str("component", "coredns").Err(err).Msg("failed to get kubernetes client")
		s.cancel()
		return fmt.Errorf("coredns: failed to get kubernetes client: %w", err)
	}

	ctx, cancel := context.WithTimeout(s.ctx, types.DefaultContextTimeout)
	defer cancel()

	cleanupLegacyResources(ctx, clientset)

	if err := createService(ctx, clientset, s.embedded.NodeIP); err != nil {
		log.Error().Str("component", "coredns").Err(err).Msg("failed to register kube-dns service")
		s.cancel()
		return fmt.Errorf("coredns: failed to register kube-dns service: %w", err)
	}
	return nil
}

func (s *service) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.instance != nil {
		s.instance.Stop()
	}
}

func newCorefileInput(corefile string) caddy.CaddyfileInput {
	return caddy.CaddyfileInput{
		Filepath:       "Corefile",
		Contents:       []byte(corefile),
		ServerTypeName: "dns",
	}
}
