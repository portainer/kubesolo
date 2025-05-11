package system

import (
	"net/http"
	"runtime"
	"time"

	"github.com/rs/zerolog/log"
)

// EnablePProfServer enables the pprof server for debugging
var EnablePProfServer = false

// StartMonitoring starts the system monitoring services including pprof and memory monitoring
func StartMonitoring() {
	if EnablePProfServer {
		go func() {
			log.Debug().Msg("Starting pprof server on :6060")
			http.ListenAndServe(":6060", nil)
		}()
	}

	go monitorMemory()
}

// monitorMemory continuously monitors memory usage and logs significant changes
func monitorMemory() {
	var m runtime.MemStats
	var lastAlloc uint64
	var lastSys uint64
	var lastNumGC uint32

	for {
		runtime.ReadMemStats(&m)
		allocDiff := int64(m.Alloc) - int64(lastAlloc)
		sysDiff := int64(m.Sys) - int64(lastSys)
		gcRan := m.NumGC > lastNumGC

		if allocDiff > 5*1024*1024 || allocDiff < -5*1024*1024 || sysDiff > 5*1024*1024 || gcRan {
			log.Debug().Msgf("Memory: Alloc=%v MiB, Sys=%v MiB, NumGC=%v",
				m.Alloc/1024/1024,
				m.Sys/1024/1024,
				m.NumGC)

			lastAlloc = m.Alloc
			lastSys = m.Sys
			lastNumGC = m.NumGC
		}
		runtime.GC()
		time.Sleep(30 * time.Second)
	}
}
