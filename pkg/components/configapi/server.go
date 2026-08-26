package configapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	httpReadTimeout  = 5 * time.Second
	httpWriteTimeout = 10 * time.Second
	httpIdleTimeout  = 30 * time.Second
	shutdownTimeout  = 5 * time.Second

	// socketMode makes the socket readable and writable only by its owner. This
	// is the whole authorisation model: anyone who can open the socket can
	// change KubeSolo's configuration.
	socketMode = 0o600

	// dialProbeTimeout bounds the check for whether an existing socket still has
	// something listening on it.
	dialProbeTimeout = time.Second
)

// Run starts the configuration API and blocks until the context is done.
func (s *Service) Run() error {
	log.Info().
		Str("component", "configapi").
		Str("socket", s.opts.SocketPath).
		Msg("starting kubesolo configuration API...")

	srv, err := s.startServer()
	if err != nil {
		log.Error().Str("component", "configapi").Err(err).Msg("failed to start configuration API")
		return err
	}

	close(s.readyCh)
	log.Info().
		Str("component", "configapi").
		Str("socket", s.opts.SocketPath).
		Msg("kubesolo configuration API is ready")

	<-s.ctx.Done()
	log.Info().Str("component", "configapi").Msg("shutting down configuration API...")

	s.shutdownServer(srv)
	s.wg.Wait()

	// The socket is a filesystem entry and does not disappear with the process.
	// Removing it on the way out is what makes the next start clean rather than
	// a reclaim.
	if err := os.Remove(s.opts.SocketPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn().Str("component", "configapi").Err(err).Msg("could not remove the configuration API socket")
	}

	log.Info().Str("component", "configapi").Msg("configuration API stopped")
	return nil
}

// startServer binds the socket and begins serving.
//
// The listener is created synchronously so that a bind failure reaches the
// caller rather than being swallowed inside a goroutine.
func (s *Service) startServer() (*http.Server, error) {
	if err := s.clearStaleSocket(); err != nil {
		return nil, err
	}

	dir := filepath.Dir(s.opts.SocketPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}

	listener, err := net.Listen("unix", s.opts.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", s.opts.SocketPath, err)
	}

	// Between Listen and Chmod the socket exists with the process umask applied.
	// net.Listen creates it 0777&^umask, so a permissive umask would leave a
	// window where anyone could connect. Narrow it immediately, and refuse to
	// serve at all if that fails.
	if err := os.Chmod(s.opts.SocketPath, socketMode); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("restrict %s to its owner: %w", s.opts.SocketPath, err)
	}

	srv := &http.Server{
		Handler:      s.routes(),
		ReadTimeout:  httpReadTimeout,
		WriteTimeout: httpWriteTimeout,
		IdleTimeout:  httpIdleTimeout,
	}

	s.wg.Go(func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Str("component", "configapi").Err(err).Msg("configuration API exited unexpectedly")
		}
	})

	return srv, nil
}

// clearStaleSocket removes a socket left behind by a process that did not shut
// down cleanly.
//
// It is deliberately narrow. Removing the path unconditionally would let a
// second KubeSolo silently steal the socket from a running first one, leaving
// the original serving a socket no longer reachable by name. So the path is
// removed only when it is a socket *and* nothing answers on it. Anything else —
// a regular file, a directory, or a socket with a live listener — is an error.
func (s *Service) clearStaleSocket() error {
	info, err := os.Lstat(s.opts.SocketPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", s.opts.SocketPath, err)
	}

	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket; refusing to replace it", s.opts.SocketPath)
	}

	if conn, err := net.DialTimeout("unix", s.opts.SocketPath, dialProbeTimeout); err == nil {
		_ = conn.Close()
		return fmt.Errorf("%s is already in use by another process", s.opts.SocketPath)
	}

	log.Info().
		Str("component", "configapi").
		Str("socket", s.opts.SocketPath).
		Msg("removing a socket left behind by a previous run")
	if err := os.Remove(s.opts.SocketPath); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", s.opts.SocketPath, err)
	}
	return nil
}

// shutdownServer closes the API gracefully. It is given its own short context
// because the parent is already done by the time this runs — that is what
// triggered the shutdown.
func (s *Service) shutdownServer(srv *http.Server) {
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Warn().Str("component", "configapi").Err(err).Msg("configuration API shutdown returned an error")
	}
}
