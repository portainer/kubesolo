package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

const (
	metricsHTTPReadTimeout  = 5 * time.Second
	metricsHTTPWriteTimeout = 10 * time.Second
	metricsHTTPIdleTimeout  = 30 * time.Second
	shutdownTimeout         = 5 * time.Second
)

// startHTTPServer binds the metrics endpoint and begins serving in a
// background goroutine. The listener is created synchronously so that bind
// errors (e.g. port-in-use) are reported eagerly to the caller rather than
// being swallowed inside a goroutine.
func (s *Service) startHTTPServer() (*http.Server, error) {
	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{
		Registry:          s.registry,
		EnableOpenMetrics: true,
		Timeout:           metricsHTTPWriteTimeout,
	}))

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintln(w, `<html><body><h1>kubesolo metrics</h1><ul><li><a href="/metrics">/metrics</a></li><li><a href="/healthz">/healthz</a></li></ul></body></html>`)
	})

	srv := &http.Server{
		Addr:         s.embedded.Metrics.BindAddress,
		Handler:      mux,
		ReadTimeout:  metricsHTTPReadTimeout,
		WriteTimeout: metricsHTTPWriteTimeout,
		IdleTimeout:  metricsHTTPIdleTimeout,
	}

	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", srv.Addr, err)
	}

	s.wg.Go(func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().
				Str("component", "metrics").
				Err(err).
				Msg("metrics HTTP server exited unexpectedly")
		}
	})

	return srv, nil
}

// shutdownHTTPServer gracefully closes the metrics HTTP server. It is given
// its own short context because the parent context is already done by the
// time we get here (that's what triggered the shutdown).
func (s *Service) shutdownHTTPServer(srv *http.Server) {
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Warn().
			Str("component", "metrics").
			Err(err).
			Msg("metrics HTTP server shutdown returned an error")
	}
}
