package kine

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	kubesoloservice "github.com/portainer/kubesolo/internal/runtime/service"
	"github.com/rs/zerolog/log"
)

// Run starts the kine service in the following order:
// 1. it ensures the database directory exists
// 2. it starts the kine server
// 3. it waits for a signal to stop the kine server
// 4. it logs the termination of the kine server
// 5. it returns an error if it fails
func (s *service) Run() error {
	log.Info().Str("component", "kine").Str("database", s.databaseDir).Msg("starting kine process (sqlite storage)...")
	if err := filesystem.EnsureDirectoryExists(s.databaseDir); err != nil {
		log.Error().Str("component", "kine").Msgf("failed to create kine database directory: %v...", err)
		s.terminate()
		return err
	}

	if err := kubesoloservice.RunServiceWithStartupCheck(func() error {
		log.Debug().Str("component", "kine").Msg("starting kine server...")
		_, err := endpoint.Listen(s.ctx, s.generateKineConfig())
		if err != nil {
			log.Error().Str("component", "kine").Msgf("failed to start kine: %v...", err)
			s.terminate()
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	log.Info().Str("component", "kine").Msg("kine server started successfully...")
	close(s.kineReady)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals

	log.Info().Str("component", "kine").Msg("received signal, stopping kine...")
	s.terminate()

	return nil
}

func (s *service) terminate() {
	log.Info().Str("component", "kine").Msg("terminating the kine process...")
	s.cancel()
}
