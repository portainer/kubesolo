package metrics

import (
	"os"
	"runtime"
	"time"

	"github.com/portainer/kubesolo/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const metricsNamespace = "kubesolo"

// componentMetrics holds the set of mutable gauges that are updated either
// by readiness-channel watchers or the periodic prober. Collectors that derive
// their value on scrape or never change (build_info, uptime, kine_db_size_bytes,
// certificate_*) are registered directly on the registry and don't need to be
// addressed after construction.
type componentMetrics struct {
	componentUp                 *prometheus.GaugeVec
	componentReadyTimestamp     *prometheus.GaugeVec
	componentLastProbeTimestamp *prometheus.GaugeVec
}

// newRegistry builds a fresh Prometheus registry containing the kubesolo
// gauges plus the standard process/Go collectors. It returns the registry
// and a handle to the gauges that change at runtime.
func newRegistry(embedded types.Embedded, build BuildInfo, startAt int64) (*prometheus.Registry, *componentMetrics) {
	reg := prometheus.NewRegistry()

	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "build_info",
		Help:      "KubeSolo build information. Always 1; metadata is in the labels.",
	}, []string{"version", "commit", "build_date", "go_version", "arch"})
	buildInfo.WithLabelValues(
		stringOrUnknown(build.Version),
		stringOrUnknown(build.Commit),
		stringOrUnknown(build.BuildDate),
		runtime.Version(),
		runtime.GOARCH,
	).Set(1)
	reg.MustRegister(buildInfo)

	startTime := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "start_time_seconds",
		Help:      "Unix timestamp at which the kubesolo metrics endpoint started.",
	})
	startTime.Set(float64(startAt))
	reg.MustRegister(startTime)

	uptime := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "uptime_seconds",
		Help:      "Seconds elapsed since the kubesolo metrics endpoint started.",
	}, func() float64 {
		return float64(time.Now().Unix() - startAt)
	})
	reg.MustRegister(uptime)

	kineDBSize := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "kine_db_size_bytes",
		Help:      "Size in bytes of the kine SQLite database file. 0 if the file cannot be stat'd.",
	}, func() float64 {
		info, err := os.Stat(kineDBPath(embedded))
		if err != nil {
			return 0
		}
		return float64(info.Size())
	})
	reg.MustRegister(kineDBSize)

	reg.MustRegister(newCertificateCollector(embedded))

	cm := &componentMetrics{
		componentUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "component_up",
			Help:      "1 if the named control plane component is reachable and healthy, 0 otherwise.",
		}, []string{"component"}),
		componentReadyTimestamp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "component_ready_timestamp_seconds",
			Help:      "Unix timestamp at which the named component first signaled readiness. 0 until ready.",
		}, []string{"component"}),
		componentLastProbeTimestamp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "component_last_probe_timestamp_seconds",
			Help:      "Unix timestamp of the most recent kubesolo health probe for the named component.",
		}, []string{"component"}),
	}
	reg.MustRegister(cm.componentUp)
	reg.MustRegister(cm.componentReadyTimestamp)
	reg.MustRegister(cm.componentLastProbeTimestamp)

	return reg, cm
}

func stringOrUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
