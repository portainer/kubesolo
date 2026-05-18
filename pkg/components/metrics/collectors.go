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
// by readiness-channel watchers or the periodic prober. Constant collectors
// (build_info, full_mode, container_mode, uptime, kine_db_size_bytes) are
// registered directly on the registry and don't need to be addressed after
// construction.
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

	containerMode := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "container_mode",
		Help:      "1 if kubesolo detects it is running inside a container, 0 otherwise.",
	})
	containerMode.Set(boolToFloat(detectContainerMode()))
	reg.MustRegister(containerMode)

	loadBalancer := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "load_balancer_enabled",
		Help:      "1 if the kubesolo load balancer mutator is enabled, 0 otherwise.",
	})
	loadBalancer.Set(boolToFloat(embedded.LoadBalancer))
	reg.MustRegister(loadBalancer)

	portainerEdge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "portainer_edge_enabled",
		Help:      "1 if Portainer Edge Agent deployment is enabled, 0 otherwise.",
	})
	portainerEdge.Set(boolToFloat(embedded.IsPortainerEdge))
	reg.MustRegister(portainerEdge)

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

// detectContainerMode mirrors the heuristic used by install.sh and reports
// whether kubesolo is running inside a container. Both Docker (/.dockerenv)
// and Podman/CRI-O (/run/.containerenv) are detected.
func detectContainerMode() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
