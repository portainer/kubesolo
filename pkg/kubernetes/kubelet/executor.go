package kubelet

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/kubernetes/cmd/kubelet/app"

	kubesoloservice "github.com/portainer/kubesolo/internal/runtime/service"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

func (s *service) Run(apiServerReady chan struct{}) error {
	log.Info().Str("component", "kubelet").Msg("starting kubelet...")
	if err := s.validation(); err != nil {
		s.terminate()
		return err
	}

	if err := s.generateKubeletKubeconfig(); err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to generate kubelet kubeconfig: %v...", err)
		s.terminate()
		return err
	}

	if err := s.writeKubeletConfigFile(); err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to write kubelet config: %v...", err)
		s.terminate()
		return err
	}

	command := app.NewKubeletCommand(context.Background())
	s.configureKubeletArgs(command)

	time.Sleep(types.DefaultComponentSleep)
	if err := kubesoloservice.RunServiceWithStartupCheck(func() error {
		<-apiServerReady
		go func() {
			if err := command.ExecuteContext(s.ctx); err != nil {
				log.Error().Str("component", "kubelet").Msgf("kubelet exited with error: %v", err)
			}
		}()
		return nil
	}); err != nil {
		return err
	}

	go func() {
		s.postSetup()
		log.Info().Str("component", "kubelet").Msg("kubelet started successfully...")
		close(s.kubeletReady)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals

	log.Info().Str("component", "kubelet").Msg("received signal, stopping kubelet...")
	s.terminate()

	return nil
}

func (s *service) postSetup() {
	if err := s.applyKubeletRBAC(); err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to apply RBAC rules: %v...", err)
		s.terminate()
		return
	}

	if err := s.checkKubeletHealth(); err != nil {
		log.Error().Str("component", "kubelet").Msgf("kubelet health check failed: %v...", err)
		s.terminate()
	}
}

func (s *service) terminate() {
	log.Info().Str("component", "kubelet").Msg("terminating kubelet...")
	s.cancel()
}
