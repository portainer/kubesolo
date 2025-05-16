package controller

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	kubesoloservice "github.com/portainer/kubesolo/internal/runtime/service"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"k8s.io/kubernetes/cmd/kube-controller-manager/app"
)

// Run starts the controller manager in the following order:
// 1. it validates the controller manager
// 2. it sets the controller manager flags
// 3. it sleeps for the default component sleep duration
// 4. it runs the controller manager
// 5. it waits for a signal to stop the controller manager
// 6. it logs the termination of the controller manager
func (s *service) Run(apiServerReadyCh chan struct{}) error {
	log.Info().Str("component", "controller").Msg("starting controller manager...")
	if err := s.validation(); err != nil {
		s.terminate()
		return err
	}

	command := app.NewControllerManagerCommand()
	command.SetArgs([]string{})
	s.configureControllerManagerFlags(command)

	time.Sleep(types.DefaultComponentSleep)
	if err := kubesoloservice.RunServiceWithStartupCheck(func() error {
		<-apiServerReadyCh
		go func() {
			if err := command.ExecuteContext(s.ctx); err != nil {
				log.Error().Str("component", "controller").Msgf("controller manager exited with error: %v", err)
			}
		}()
		return nil
	}); err != nil {
		return err
	}

	go func() {
		s.postSetup()
		log.Info().Str("component", "controller").Msg("controller manager ready...")
		close(s.controllerReady)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals

	log.Info().Str("component", "controller").Msg("received signal, stopping controller manager...")
	s.terminate()

	return nil
}

func (s *service) postSetup() {
	if err := s.checkControllerManagerHealth(); err != nil {
		log.Error().Str("component", "controller").Msgf("controller manager health check failed: %v", err)
		s.terminate()
	}
}

func (s *service) terminate() {
	log.Info().Str("component", "controller").Msg("terminating controller manager...")
	s.cancel()
}
