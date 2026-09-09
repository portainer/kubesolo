package kubeproxy

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/portainer/kubesolo/internal/runtime/network"
	kubesoloservice "github.com/portainer/kubesolo/internal/runtime/service"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"

	proxy "k8s.io/kubernetes/cmd/kube-proxy/app"
)

// Run starts the kube proxy in the following order:
// 1. it sets the kube proxy flags
// 2. it sleeps for the default component sleep duration
// 3. it runs the kube proxy
// 4. it waits for a signal to stop the kube proxy
// 5. it logs the termination of the kube proxy
func (s *service) Run(kubeletReadyCh chan struct{}) error {
	log.Info().Str("component", "kubeproxy").Msg("starting kubeproxy...")

	command := proxy.NewProxyCommand()
	command.SetArgs([]string{})
	s.configureKubeProxyFlags(command)

	time.Sleep(types.DefaultComponentSleep)
	if err := kubesoloservice.RunServiceWithStartupCheck(func() error {
		<-kubeletReadyCh
		s.wg.Go(func() {
			if err := command.ExecuteContext(s.ctx); err != nil {
				log.Error().Str("component", "kubeproxy").Msgf("kubeproxy exited with error: %v", err)
			}
		})
		return nil
	}); err != nil {
		return err
	}

	s.wg.Go(func() {
		if err := s.postSetup(); err != nil {
			return
		}
		log.Info().Str("component", "kubeproxy").Msg("kubeproxy started successfully...")
		close(s.kubeproxyReady)
	})

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals

	log.Info().Str("component", "kubeproxy").Msg("received signal, stopping kubeproxy...")
	s.terminate()

	return nil
}

func (s *service) postSetup() error {
	if err := s.checkKubeProxyHealth(); err != nil {
		log.Error().Str("component", "kubeproxy").Msgf("kubeproxy health check failed: %v...", err)
		s.cancelShutdown()
		return err
	}
	// Re-verify masquerade is in place: kube-proxy programs its own chains in
	// the nat table on startup, so this ensures kubeproxyReady only fires once
	// SNAT for pod egress is confirmed.
	if err := network.EnsurePodMasquerade(types.DefaultPodCIDR); err != nil {
		log.Error().Str("component", "kubeproxy").Msgf("failed to ensure pod masquerade: %v", err)
	}
	return nil
}

// cancelShutdown cancels the service context. Use from goroutines started with s.wg.Go;
// terminate() must not be called from those goroutines (it waits on s.wg and deadlocks).
func (s *service) cancelShutdown() {
	log.Info().Str("component", "kubeproxy").Msg("canceling kubeproxy...")
	s.cancel()
}

func (s *service) terminate() {
	log.Info().Str("component", "kubeproxy").Msg("terminating kubeproxy...")
	s.cancel()
	s.wg.Wait() // Wait for all goroutines to complete
}
