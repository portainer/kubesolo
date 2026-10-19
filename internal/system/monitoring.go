package system

import (
	"net"
	"net/http"
	"strconv"

	"github.com/rs/zerolog/log"
)

// StartMonitoring starts the system monitoring services including pprof and memory
// monitoring, pprof on port (logging.pprofPort) on every interface
func StartMonitoring(port int) {
	addr := net.JoinHostPort("", strconv.Itoa(port))
	go func() {
		log.Debug().Msgf("Starting pprof server on %s", addr)
		_ = http.ListenAndServe(addr, nil)
	}()
}
