package kubelet

import (
	"fmt"
	"os"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
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

		"resolvConf":        "/etc/resolv.conf",
		"tlsCertFile":       s.certFile,
		"tlsPrivateKeyFile": s.keyFile,

		"cgroupDriver": "systemd",

		"registerNode":                   true,
		"readOnlyPort":                   0,
		"port":                           10250,
		"streamingConnectionIdleTimeout": "1h0m0s",
		"rotateCertificates":             true,

		"registerWithTaints": []map[string]any{},

		"evictionHard": map[string]string{
			"memory.available": "25Mi",
			"nodefs.available": "200Mi",
		},
		"systemReserved": map[string]string{"memory": "25Mi"},
		"kubeReserved":   map[string]string{"memory": "25Mi"},
		"failSwapOn":     false,

		"kubeAPIQPS":                1,
		"kubeAPIBurst":              2,
		"serializeImagePulls":       true,
		"workerLoopSize":            1,  // Limit concurrent worker threads
		"imagePullProgressDeadline": "1m",

		"imageGCHighThresholdPercent": 90,
		"imageGCLowThresholdPercent":  75,
		"registryPullQPS":             1,
		"registryBurst":               1,

		"eventRecordQPS": 1,
		"eventBurst":     1,

		"containerLogMaxSize":     "512Ki",
		"enableProfilingHandler":  false,
		"enableDebugFlagsHandler": false,
		"maxPods":                 20,

		"featureGates": map[string]bool{
			"RotateKubeletServerCertificate": true,
		},
	}
}
