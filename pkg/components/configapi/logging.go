package configapi

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// requestLog accumulates what is worth recording about one request. The
// middleware creates it; a handler adds to it as it learns more.
type requestLog struct {
	// changed lists the settings a write altered, by config path. Paths only —
	// a value is never recorded, so an audit line says that the Portainer edge
	// key was replaced without disclosing it.
	changed []string

	// restartRequired reports whether the change needs a restart to take effect.
	restartRequired bool

	// failure is the message returned to the client, when the request failed.
	failure string
}

type requestLogKey struct{}

// requestLogFrom returns the record for the request in flight, or nil when the
// handler is called outside the middleware (only in tests).
func requestLogFrom(ctx context.Context) *requestLog {
	rl, _ := ctx.Value(requestLogKey{}).(*requestLog)
	return rl
}

// statusRecorder captures the status code, which http.ResponseWriter does not
// otherwise expose.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// withLogging records every request against the configuration API.
//
// This is an audit trail as much as a debugging aid: the endpoint changes how
// KubeSolo will start, and until now nothing recorded that it had been used.
//
// Reads are logged at debug and writes at info, so a user interface polling the
// configuration does not bury the changes among its own reads. Health checks are
// not logged at all — they carry no information and would be the loudest thing
// in the journal.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		rl := &requestLog{}
		recorder := &statusRecorder{ResponseWriter: w}
		started := time.Now()

		next.ServeHTTP(recorder, r.WithContext(context.WithValue(r.Context(), requestLogKey{}, rl)))

		level := zerolog.InfoLevel
		if r.Method == http.MethodGet {
			level = zerolog.DebugLevel
		}
		// A rejected request is worth seeing whatever it was trying to do.
		if recorder.status >= http.StatusBadRequest {
			level = zerolog.WarnLevel
		}

		event := log.WithLevel(level).
			Str("component", "configapi").
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", recorder.status).
			Dur("took", time.Since(started))

		if len(rl.changed) > 0 {
			event = event.Strs("changed", rl.changed).Bool("restart_required", rl.restartRequired)
		}
		if rl.failure != "" {
			event = event.Str("reason", rl.failure)
		}

		event.Msg("configuration API request")
	})
}
