package webhook

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"

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

// Start starts the webhook server
func (s *Service) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", s.serveMutate)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", types.DefaultWebhookPort),
		ReadTimeout:  types.DefaultWebhookReadWriteTimeout,
		WriteTimeout: types.DefaultWebhookReadWriteTimeout,
		IdleTimeout:  types.DefaultWebhookIdleTimeout,
		Handler:      mux,
	}

	log.Info().Str("component", "webhook").Msgf("starting webhook server on :%d", types.DefaultWebhookPort)

	s.startServer()
	s.handleShutdown(ctx)

	return nil
}

// startServer starts the webhook server
func (s *Service) startServer() {
	certPath := filepath.Join(s.pkiPath, "webhook", "webhook.crt")
	keyPath := filepath.Join(s.pkiPath, "webhook", "webhook.key")

	s.wg.Go(func() {
		if err := s.server.ListenAndServeTLS(certPath, keyPath); err != nil && err != http.ErrServerClosed {
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
