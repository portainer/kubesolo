package system

import (
	"net/http"

	"github.com/rs/zerolog/log"
)

// StartMonitoring starts the system monitoring services including pprof and memory monitoring
func StartMonitoring() {
	go func() {
		log.Debug().Msg("Starting pprof server on :6060")
		http.ListenAndServe(":6060", nil)
	}()
}
