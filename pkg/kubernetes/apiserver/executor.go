package apiserver

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	kubesoloservice "github.com/portainer/kubesolo/internal/runtime/service"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/kubernetes/cmd/kube-apiserver/app"
)

// Run starts the API server in the following order:
// 1. it generates the service account key
// 2. it registers the admission plugins
// 3. it sets the API server flags
// 4. it starts the API server
// 5. it waits for a signal to stop the API server
// 6. it logs the termination of the API server
func (s *service) Run(kineReadyCh chan struct{}) error {
	log.Info().Str("component", "apiserver").Msg("starting API server...")
	if err := s.generateServiceAccountKey(); err != nil {
		log.Error().Str("component", "apiserver").Msgf("failed to generate the service account key: %v...", err)
	}

	register(admission.NewPlugins(), s.nodeName)

	command := app.NewAPIServerCommand(nil)
	command.SetArgs([]string{})
	if err := s.configureAPIServerFlags(command); err != nil {
		log.Error().Str("component", "apiserver").Msgf("failed to configure API server flags: %v...", err)
		s.terminate()
		return err
	}

	if err := s.kubeSoloWebhook.start(s.ctx); err != nil {
		log.Error().Str("component", "apiserver").Msgf("failed to start kubesolo webhook: %v...", err)
		s.terminate()
		return err
	}

	time.Sleep(types.DefaultComponentSleep)
	if err := kubesoloservice.RunServiceWithStartupCheck(func() error {
		<-kineReadyCh
		go func() {
			if err := command.ExecuteContext(s.ctx); err != nil {
				log.Error().Str("component", "apiserver").Msgf("API server exited with error: %v", err)
			}
		}()
		return nil
	}); err != nil {
		return err
	}

	go func() {
		s.postSetup()
		log.Info().Str("component", "apiserver").Msg("API server ready...")
		close(s.apiServerReady)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals

	log.Info().Str("component", "apiserver").Msg("received signal, stopping the API server...")
	s.terminate()

	return nil
}

func (s *service) postSetup() {
	err := s.checkAPIServerHealth()
	if err != nil {
		log.Error().Str("component", "apiserver").Msgf("API server failed to start: %v...", err)
		s.terminate()
		return
	}

	if err := s.generateKubeConfig(); err != nil {
		log.Error().Str("component", "apiserver").Msgf("failed to generate the kubeconfig: %v...", err)
	}

	if err := s.applyAPIServerRBAC(); err != nil {
		log.Error().Str("component", "apiserver").Msgf("failed to apply the RBAC configuration: %v...", err)
	}

	if err := s.kubeSoloWebhook.RegisterWebhook(s.adminKubeconfig); err != nil {
		log.Error().Str("component", "apiserver").Msgf("failed to register the kubesolo webhook: %v...", err)
	}
}

func (s *service) terminate() {
	log.Info().Str("component", "apiserver").Msg("terminating the API server...")
	s.cancel()
}
