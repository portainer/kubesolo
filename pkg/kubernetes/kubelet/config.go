package kubelet

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/internal/runtime/network"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

// cpuManagerCheckpointFile is where the kubelet records the CPU manager policy and
// the shared CPU pool, under the kubelet root directory.
const cpuManagerCheckpointFile = "cpu_manager_state"

// cpuManagerSettings are the kubelet config fields that invalidate the CPU manager
// checkpoint when they change.
type cpuManagerSettings struct {
	Policy       string            `yaml:"cpuManagerPolicy"`
	Options      map[string]string `yaml:"cpuManagerPolicyOptions"`
	ReservedCPUs string            `yaml:"reservedSystemCPUs"`
}

// cgroupDriver returns "systemd" only when systemd is the active init system,
// otherwise "cgroupfs". Alpine Linux uses OpenRC and has no systemd even when
// cgroupv2 is present, so the systemd cgroup manager must not be used there.
func cgroupDriver() string {
	if _, err := os.Stat("/run/systemd/private"); err == nil {
		return "systemd"
	}
	return "cgroupfs"
}

// resolveCgroupDriver returns the cgroup driver the kubelet must use. A container
// runtime managed by the host reports its own driver over CRI and the kubelet has to
// match it: a mismatch lets pods start and then evicts them with errors that never
// name the cause. Without a reported driver, the driver is detected from the host.
func (s *service) resolveCgroupDriver() string {
	if s.runtimeCgroupDriver != "" {
		log.Info().Str("component", "kubelet").Str("driver", s.runtimeCgroupDriver).Msg("using the cgroup driver reported by the container runtime")
		return s.runtimeCgroupDriver
	}

	return cgroupDriver()
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

	s.invalidateCPUManagerCheckpoint(yamlConfig)

	configFile, err := os.Create(s.kubeletConfigFile)
	if err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to create config file: %v", err)
		return err
	}
	defer func() { _ = configFile.Close() }()

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

		"containerRuntimeEndpoint": s.runtimeEndpoint,

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

		"cgroupDriver": s.resolveCgroupDriver(),

		"readOnlyPort":       0,
		"rotateCertificates": true,

		"failSwapOn": false,
	}

	if s.cpuManager.Policy == types.CPUManagerPolicyStatic {
		config["cpuManagerPolicy"] = s.cpuManager.Policy
		config["reservedSystemCPUs"] = s.cpuManager.ReservedCPUs
		if len(s.cpuManager.PolicyOptions) > 0 {
			config["cpuManagerPolicyOptions"] = s.cpuManager.PolicyOptions
		}
	}

	if s.containerMode {
		// In a container cgroupv2 domain controllers block creating the
		// kubepods/system/kube cgroup hierarchies required for QoS management.
		// Disable QoS cgroups and node-allocatable enforcement; containerd/runc
		// still manage per-container cgroups normally.
		config["cgroupsPerQOS"] = false
		config["enforceNodeAllocatable"] = []string{}
		config["imageGCHighThresholdPercent"] = 100
		config["evictionHard"] = map[string]string{
			"memory.available":  "50Mi",
			"nodefs.available":  "0%",
			"nodefs.inodesFree": "0%",
			"imagefs.available": "0%",
		}
		config["systemReserved"] = map[string]string{}
		config["kubeReserved"] = map[string]string{}
		return config
	}

	return config
}

// invalidateCPUManagerCheckpoint removes the CPU manager checkpoint when the CPU
// manager settings differ from the config written on the previous start. The kubelet
// refuses to start against a checkpoint that disagrees with its config, and upstream's
// remedy is to drain the node and delete the file — which a single node cannot do, so
// kubesolo removes it instead.
//
// Both sides of the comparison are read back out of the generated YAML so they cannot
// drift from what generateKubeletConfig actually writes.
func (s *service) invalidateCPUManagerCheckpoint(newConfig []byte) {
	previous, err := os.ReadFile(s.kubeletConfigFile)
	if err != nil {
		return
	}

	if reflect.DeepEqual(readCPUManagerSettings(previous), readCPUManagerSettings(newConfig)) {
		return
	}

	checkpoint := filepath.Join(s.kubeletDir, cpuManagerCheckpointFile)
	if err := os.Remove(checkpoint); err != nil {
		if !os.IsNotExist(err) {
			log.Error().Str("component", "kubelet").Msgf("failed to remove stale cpu manager checkpoint %s: %v", checkpoint, err)
		}
		return
	}

	log.Warn().Str("component", "kubelet").Msgf("cpu manager settings changed, removed %s. workloads holding exclusive cores return to the shared pool until they are restarted", checkpoint)
}

func readCPUManagerSettings(config []byte) cpuManagerSettings {
	var settings cpuManagerSettings
	_ = yaml.Unmarshal(config, &settings)
	return settings
}
