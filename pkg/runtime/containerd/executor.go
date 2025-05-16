package containerd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/containerd/command"
	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	kubesoloservice "github.com/portainer/kubesolo/internal/runtime/service"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// Run starts the containerd service in the following order:
// 1. it validates the containerd
// 2. it writes the containerd config
// 3. it starts the containerd
// 4. it waits for a signal to stop the containerd
// 5. it logs the termination of the containerd
func (s *service) Run() error {
	log.Info().Str("component", "containerd").Str("config", s.containerdConfigFile).Msg("starting containerd...")
	if err := s.validation(); err != nil {
		s.terminate()
		return err
	}

	if err := s.writeContainerdConfigFile(); err != nil {
		log.Error().Str("component", "containerd").Msgf("failed to write config file: %v...", err)
		s.terminate()
		return err
	}

	app := command.App()
	app.Flags = s.generateCustomFlags()
	if err := kubesoloservice.RunServiceWithStartupCheck(func() error {
		go func() {
			if err := app.Run(nil); err != nil {
				log.Error().Str("component", "containerd").Msgf("failed to start containerd: %v...", err)
				s.terminate()
			}
		}()
		return nil
	}); err != nil {
		return err
	}

	go func() {
		s.postSetup()
		close(s.containerdReady)
	}()
	log.Info().Str("component", "containerd").Msg("containerd started successfully...")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals

	log.Debug().Str("component", "containerd").Msg("received signal, stopping containerd")
	s.terminate()

	return nil
}

func (s *service) postSetup() {
	log.Debug().Str("component", "containerd").Msg("waiting for containerd to be ready...")
	ctx, cancel := context.WithTimeout(context.Background(), types.DefaultContextTimeout)
	defer cancel()

	client, err := client.New(s.containerdSocketFile)
	if err != nil {
		s.terminate()
	}
	defer client.Close()

	if err := s.checkContainerdHealth(ctx, client); err != nil {
		log.Error().Str("component", "containerd").Msgf("containerd health check failed: %v...", err)
		s.terminate()
		return
	}

	log.Debug().Str("component", "containerd").Msg("containerd health check passed... now creating containerd socket link")
	if err := filesystem.EnsureSymbolicLink(s.containerdSocketFile, types.DefaultSystemContainerdSock); err != nil {
		log.Error().Str("component", "containerd").Msgf("failed to create containerd socket link: %v...", err)
		s.terminate()
		return
	}

	if err := s.ensureK8sNamespace(ctx, client); err != nil {
		log.Error().Str("component", "containerd").Msgf("failed to ensure k8s.io namespace: %v...", err)
		s.terminate()
	}

	if err := s.importImages(ctx, client, s.isPortainerEdge); err != nil {
		log.Error().Str("component", "containerd").Msgf("failed to import images: %v...", err)
		s.terminate()
	}
}

func (s *service) terminate() {
	log.Info().Str("component", "containerd").Msg("terminating containerd...")
	s.cancel()
}
