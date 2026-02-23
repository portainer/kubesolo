package kubelet

import (
	"fmt"
	"os"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

func (s *service) writeKubeletConfigFile() error {
	if err := filesystem.EnsureDirectoryExists(s.kubeletConfigDir); err != nil {
		return fmt.Errorf("failed to create kubelet directory: %v", err)
	}

	yamlConfig, err := yaml.Marshal(s.generateKubeletConfig())
	if err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to marshal kubelet config: %v", err)
		return err
	}

	configFile, err := os.Create(s.kubeletConfigFile)
	if err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to create config file: %v", err)
		return err
	}
	defer configFile.Close()

	_, err = configFile.Write(yamlConfig)
	if err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to write config file: %v", err)
		return err
	}

	log.Debug().Str("component", "kubelet").Msgf("wrote kubelet config to %s", s.kubeletConfigFile)

	return nil
}

func (s *service) generateKubeletConfig() map[string]any {
	cgroupDriver := "systemd"
	if s.containerMode {
		cgroupDriver = "cgroupfs"
	}

	evictionHard := map[string]string{
		"memory.available": "75Mi",
		"nodefs.available": "50Mi",
	}
	imageGCHigh := 95
	systemReserved := map[string]string{"memory": "25Mi"}
	kubeReserved := map[string]string{"memory": "25Mi"}
	enforceNodeAllocatable := []string{"pods"}
	cgroupsPerQOS := true
	if s.containerMode {
		evictionHard = map[string]string{
			"memory.available":  "50Mi",
			"nodefs.available":  "0%",
			"nodefs.inodesFree": "0%",
			"imagefs.available": "0%",
		}
		imageGCHigh = 100
		// In a container, we cannot create the kubepods/system/kube cgroup hierarchies
		// because cgroupv2 domain controllers block subtree creation.
		// Disable QoS cgroup management and node allocatable enforcement entirely.
		// Per-container cgroups are still managed by containerd/runc.
		cgroupsPerQOS = false
		enforceNodeAllocatable = []string{}
		systemReserved = map[string]string{}
		kubeReserved = map[string]string{}
	}

	return map[string]any{
		"kind":         "KubeletConfiguration",
		"apiVersion":   "kubelet.config.k8s.io/v1beta1",
		"enableServer": true,

		"containerRuntimeEndpoint": "unix://" + s.containerdSockFile,
		"imageServiceEndpoint":     "unix://" + s.containerdSockFile,

		"authentication": map[string]any{
			"anonymous": map[string]any{
				"enabled": false,
			},
			"webhook": map[string]any{
				"enabled":  true,
				"cacheTTL": "5m0s",
			},
			"x509": map[string]any{
				"clientCAFile": s.caFile,
			},
		},
		"authorization": map[string]any{
			"mode": "Webhook",
			"webhook": map[string]any{
				"cacheAuthorizedTTL":   "10m0s",
				"cacheUnauthorizedTTL": "1m0s",
			},
		},

		"clusterDomain": "cluster.local",
		"clusterDNS":    []string{types.DefaultCoreDNSIP},

		"resolvConf":        network.GetHostResolvConf(s.kubeletDir, s.containerMode),
		"tlsCertFile":       s.certFile,
		"tlsPrivateKeyFile": s.keyFile,

		"cgroupDriver":  cgroupDriver,
		"cgroupsPerQOS": cgroupsPerQOS,

		"enforceNodeAllocatable": enforceNodeAllocatable,

		"registerNode":                   true,
		"readOnlyPort":                   0,
		"port":                           10250,
		"syncFrequency":                  "5m0s",
		"fileCheckFrequency":             "2m0s",
		"httpCheckFrequency":             "2m0s",
		"nodeStatusUpdateFrequency":      "60s",
		"nodeStatusReportFrequency":      "15m0s",
		"volumeStatsAggPeriod":           "5m0s",
		"imageMinimumGCAge":              "10m0s",
		"imageMaximumGCAge":              "0s",
		"imageGCHighThresholdPercent":    imageGCHigh,
		"imageGCLowThresholdPercent":     80,
		"runtimeRequestTimeout":          "60s",
		"cpuManagerReconcilePeriod":      "60s",
		"streamingConnectionIdleTimeout": "1h0m0s",
		"rotateCertificates":             true,

		"registerWithTaints": []map[string]any{},

		"evictionHard":   evictionHard,
		"systemReserved": systemReserved,
		"kubeReserved":   kubeReserved,
		"failSwapOn":     false,

		"kubeAPIQPS":                10,
		"kubeAPIBurst":              20,
		"serializeImagePulls":       true,
		"imagePullProgressDeadline": "1m",

		"registryPullQPS": 5,
		"registryBurst":   10,

		"eventRecordQPS": 5,
		"eventBurst":     10,

		"containerLogMaxSize":     "512Ki",
		"enableProfilingHandler":  false,
		"enableDebugFlagsHandler": false,
		"maxPods":                 20,

		"featureGates": map[string]bool{
			"RotateKubeletServerCertificate": true,
		},
	}
}

func (s *service) resolveConfPath() string {
	if s.containerMode {
		return "/dev/null"
	}
	return "/etc/resolv.conf"
}
