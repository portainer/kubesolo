package kine

import (
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/k3s-io/kine/pkg/logstructured"
	"github.com/k3s-io/kine/pkg/server"
	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	kubesoloservice "github.com/portainer/kubesolo/internal/runtime/service"
	"github.com/portainer/kubesolo/pkg/kine/memlog"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"go.etcd.io/etcd/server/v3/embed"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// Run starts the kine service.
// When inMemory is enabled it uses an experimental in-memory store;
// otherwise it falls back to the default SQLite backend.
func (s *service) Run() error {
	if s.inMemory {
		return s.runInMemory()
	}
	return s.runSQLite()
}

// runSQLite starts the kine service backed by SQLite (default).
func (s *service) runSQLite() error {
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

// runInMemory starts the kine service backed by an experimental in-memory store.
func (s *service) runInMemory() error {
	log.Warn().Str("component", "kine").Msg("starting kine process (experimental in-memory storage) - data will NOT persist across restarts")

	if err := kubesoloservice.RunServiceWithStartupCheck(func() error {
		ml := memlog.New(compactInterval, compactBatchSize, compactMinRetain)
		backend := logstructured.New(ml)

		grpcServer := grpc.NewServer(
			grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
				MinTime:             embed.DefaultGRPCKeepAliveMinTime,
				PermitWithoutStream: false,
			}),
			grpc.KeepaliveParams(keepalive.ServerParameters{
				Time:    embed.DefaultGRPCKeepAliveInterval,
				Timeout: embed.DefaultGRPCKeepAliveTimeout,
			}),
		)

		if err := backend.Start(s.ctx); err != nil {
			log.Error().Str("component", "kine").Msgf("failed to start kine backend: %v...", err)
			s.terminate()
			return err
		}

		bridge := server.New(backend, "http", notifyInterval, "")
		bridge.Register(grpcServer)

		listener, err := net.Listen("tcp", types.DefaultKineEndpoint)
		if err != nil {
			log.Error().Str("component", "kine").Msgf("failed to listen on %s: %v...", types.DefaultKineEndpoint, err)
			s.terminate()
			return err
		}

		go func() {
			<-s.ctx.Done()
			grpcServer.GracefulStop()
		}()

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			if err := grpcServer.Serve(listener); err != nil {
				log.Error().Str("component", "kine").Msgf("kine gRPC server exited: %v", err)
			}
		}()

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
	s.wg.Wait()
}
