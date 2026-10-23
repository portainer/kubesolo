package webhook

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// RegisterWebhook registers the webhook with the Kubernetes API server
func (w *Service) RegisterWebhook() error {
	clientset, err := kubesolokubernetes.GetKubernetesClient(w.adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %v", err)
	}
	w.clientsetMu.Lock()
	w.clientset = clientset
	w.clientsetMu.Unlock()

	webhookConfig, err := w.createConfiguration()
	if err != nil {
		return err
	}

	return w.createOrUpdateConfig(webhookConfig)
}

// address is where the webhook listens: loopback, since only the API server,
// in this same process, calls it.
func (s *Service) address() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port))
}

// Start starts the webhook server.
//
// The port is bound here rather than in the serving goroutine, so that a port
// already in use fails the start. The webhook fails open: were it simply
// missing, the API server would admit every pod without a node — KubeSolo runs
// no scheduler — and the whole cluster would sit Pending with nothing saying
// why.
func (s *Service) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", s.serveMutate)

	s.server = &http.Server{
		Addr:         s.address(),
		ReadTimeout:  types.DefaultWebhookReadWriteTimeout,
		WriteTimeout: types.DefaultWebhookReadWriteTimeout,
		IdleTimeout:  types.DefaultWebhookIdleTimeout,
		Handler:      mux,
	}

	listener, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("webhook server cannot listen on %s (kubernetes.webhook.port): %w", s.server.Addr, err)
	}

	log.Info().Str("component", "webhook").Msgf("starting webhook server on %s", s.server.Addr)

	s.startServer(listener)
	s.handleShutdown(ctx)

	return nil
}

// startServer serves the webhook on its listener
func (s *Service) startServer(listener net.Listener) {
	certPath := filepath.Join(s.pkiPath, "webhook", "webhook.crt")
	keyPath := filepath.Join(s.pkiPath, "webhook", "webhook.key")

	s.wg.Go(func() {
		if err := s.server.ServeTLS(listener, certPath, keyPath); err != nil && err != http.ErrServerClosed {
			log.Error().Str("component", "webhook").Err(err).Msg("webhook server failed")
		}
	})
}

// handleShutdown handles the shutdown of the webhook server
func (s *Service) handleShutdown(ctx context.Context) {
	s.wg.Go(func() {
		<-ctx.Done()
		if err := s.server.Shutdown(context.Background()); err != nil {
			log.Error().Str("component", "webhook").Err(err).Msg("error shutting down webhook server")
		}
	})
}
