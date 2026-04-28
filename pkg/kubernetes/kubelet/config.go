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

// cgroupDriver returns "systemd" only when systemd is the active init system,
// otherwise "cgroupfs". Alpine Linux uses OpenRC and has no systemd even when
// cgroupv2 is present, so the systemd cgroup manager must not be used there.
func cgroupDriver() string {
	if _, err := os.Stat("/run/systemd/private"); err == nil {
		return "systemd"
	}
	return "cgroupfs"
}

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
	config := map[string]any{
		"kind":       "KubeletConfiguration",
		"apiVersion": "kubelet.config.k8s.io/v1beta1",

		"containerRuntimeEndpoint": "unix://" + s.containerdSockFile,

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

		"resolvConf":        network.GetHostResolvConf(s.kubeletDir),
		"tlsCertFile":       s.certFile,
		"tlsPrivateKeyFile": s.keyFile,

		"cgroupDriver": cgroupDriver(),

		"readOnlyPort":       0,
		"rotateCertificates": true,

		"failSwapOn": false,
	}

	// Edge-optimised overrides — only applied when not in full mode.
	// When full mode is enabled, upstream Kubernetes defaults are used instead.
	if !s.fullMode {
		config["enableProfilingHandler"] = false
		config["enableDebugFlagsHandler"] = false
		config["streamingConnectionIdleTimeout"] = "1h0s"
		config["syncFrequency"] = "5m0s"
		config["fileCheckFrequency"] = "2m0s"
		config["httpCheckFrequency"] = "2m0s"
		config["nodeStatusUpdateFrequency"] = "60s"
		config["nodeStatusReportFrequency"] = "15m0s"
		config["volumeStatsAggPeriod"] = "5m0s"
		config["imageMinimumGCAge"] = "10m0s"
		config["imageMaximumGCAge"] = "0s"
		config["imageGCHighThresholdPercent"] = 95
		config["runtimeRequestTimeout"] = "60s"
		config["cpuManagerReconcilePeriod"] = "60s"
		config["kubeAPIQPS"] = 10
		config["kubeAPIBurst"] = 20
		config["eventRecordQPS"] = 5
		config["eventBurst"] = 10
		config["containerLogMaxSize"] = "512Ki"
		config["maxPods"] = 20
		config["evictionHard"] = map[string]string{
			"memory.available": "75Mi",
			"nodefs.available": "50Mi",
		}
		config["systemReserved"] = map[string]string{"memory": "25Mi"}
		config["kubeReserved"] = map[string]string{"memory": "25Mi"}
	}

	return config
}
