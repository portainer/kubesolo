package metrics

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// Run starts the metrics service. It builds the Prometheus registry,
// starts the HTTP server, watches readiness channels to flip per-component
// "up" gauges, and runs a periodic prober. It blocks until ctx is done.
func (s *Service) Run() error {
	log.Info().
		Str("component", "metrics").
		Str("bind-address", s.embedded.Metrics.BindAddress).
		Msg("starting kubesolo metrics endpoint...")

	s.startAt = time.Now().Unix()
	s.registry, s.metrics = newRegistry(s.embedded, s.build, s.startAt)

	s.watchReadyChannels()

	s.wg.Go(s.runProberLoop)

	srv, err := s.startHTTPServer()
	if err != nil {
		log.Error().Str("component", "metrics").Err(err).Msg("failed to start metrics HTTP server")
		return err
	}

	close(s.readyCh)
	log.Info().
		Str("component", "metrics").
		Str("bind-address", s.embedded.Metrics.BindAddress).
		Msg("kubesolo metrics endpoint is ready")

	<-s.ctx.Done()
	log.Info().Str("component", "metrics").Msg("shutting down metrics endpoint...")

	s.shutdownHTTPServer(srv)
	s.wg.Wait()

	log.Info().Str("component", "metrics").Msg("metrics endpoint stopped")
	return nil
}

// watchReadyChannels spawns a small goroutine per component that flips
// kubesolo_component_up to 1 and stamps the ready timestamp the moment the
// component's readiness channel is closed. Channels that close before the
// metrics service starts are handled correctly because select-on-closed-chan
// returns immediately.
//
// It also pre-creates the per-component label set on every component_* vec so
// that the metric labels are visible on the very first scrape, even for
// components without an active prober (e.g. kubeproxy).
func (s *Service) watchReadyChannels() {
	for name, ch := range s.componentReady {
		s.metrics.componentUp.WithLabelValues(name).Set(0)
		s.metrics.componentReadyTimestamp.WithLabelValues(name).Set(0)
		s.metrics.componentLastProbeTimestamp.WithLabelValues(name).Set(0)

		name, ch := name, ch
		s.wg.Go(func() {
			select {
			case <-ch:
				s.metrics.componentUp.WithLabelValues(name).Set(1)
				s.metrics.componentReadyTimestamp.WithLabelValues(name).Set(float64(time.Now().Unix()))
				log.Debug().
					Str("component", "metrics").
					Str("target", name).
					Msg("component reported ready, flipping up gauge")
			case <-s.ctx.Done():
				return
			}
		})
	}
}

// runProberLoop refreshes per-component "up" gauges by actively probing each
// component's health endpoint or socket on a fixed interval. The first probe
// runs immediately so the metrics endpoint reflects current state on first
// scrape rather than waiting up to one interval.
func (s *Service) runProberLoop() {
	interval := types.DefaultMetricsProbeInterval
	probers := s.buildProbers()

	s.runProbes(probers)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.runProbes(probers)
		}
	}
}

// runProbes runs every prober once and updates the corresponding gauges.
// Each probe runs in its own short-lived goroutine so a slow probe cannot
// block the others; we wait for all of them before returning so the next
// scrape sees a consistent set of values.
func (s *Service) runProbes(probers map[string]prober) {
	now := float64(time.Now().Unix())

	var wg sync.WaitGroup
	for name, probe := range probers {
		name, probe := name, probe
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			defer cancel()

			err := probe(ctx)
			if err != nil {
				s.metrics.componentUp.WithLabelValues(name).Set(0)
				log.Debug().
					Str("component", "metrics").
					Str("target", name).
					Err(err).
					Msg("component probe failed")
			} else {
				s.metrics.componentUp.WithLabelValues(name).Set(1)
			}
			s.metrics.componentLastProbeTimestamp.WithLabelValues(name).Set(now)
		}()
	}
	wg.Wait()
}

// kineDBPath returns the absolute path to the kine SQLite database file.
// It is used by the kine_db_size_bytes collector.
func kineDBPath(embedded types.Embedded) string {
	return filepath.Join(embedded.KineDir, types.DefaultKineDBFile)
}
