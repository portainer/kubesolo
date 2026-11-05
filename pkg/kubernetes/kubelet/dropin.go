package kubelet

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

// dropInExtension is the only file extension the kubelet loads from --config-dir.
const dropInExtension = ".conf"

// refusedDropInKeys are the CPU manager settings. KubeSolo removes the CPU manager
// checkpoint when they change in its generated config, but it cannot see a change made
// in a drop-in, and the kubelet refuses to start against a checkpoint that disagrees
// with its config.
var refusedDropInKeys = []string{"cpuManagerPolicy", "cpuManagerPolicyOptions", "reservedSystemCPUs"}

// kubesoloDropInKeys are settings KubeSolo sets itself. A drop-in may override them, but
// the node is unlikely to work afterwards.
var kubesoloDropInKeys = []string{
	"containerRuntimeEndpoint",
	"authentication",
	"authorization",
	"tlsCertFile",
	"tlsPrivateKeyFile",
	"cgroupDriver",
	"clusterDNS",
	"clusterDomain",
	"resolvConf",
}

// containerModeDropInKeys are settings KubeSolo turns off in container mode, where the
// kubelet cannot create the QoS cgroup hierarchy.
var containerModeDropInKeys = []string{"cgroupsPerQOS", "enforceNodeAllocatable"}

// checkDropIns checks the drop-ins the kubelet will merge on top of the generated
// config. Like the kubelet, it walks every file below the drop-in directory.
func (s *service) checkDropIns() error {
	return filepath.WalkDir(s.kubeletDropInDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != dropInExtension {
			log.Warn().Str("component", "kubelet").Msgf("ignoring %s: the kubelet only loads drop-ins ending in %s", path, dropInExtension)
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read kubelet drop-in %s: %w", path, err)
		}

		var dropIn map[string]any
		if err := yaml.Unmarshal(data, &dropIn); err != nil {
			return fmt.Errorf("kubelet drop-in %s is not valid YAML: %w", path, err)
		}
		if dropIn["apiVersion"] != "kubelet.config.k8s.io/v1beta1" || dropIn["kind"] != "KubeletConfiguration" {
			return fmt.Errorf("kubelet drop-in %s must set apiVersion: kubelet.config.k8s.io/v1beta1 and kind: KubeletConfiguration", path)
		}

		for _, key := range refusedDropInKeys {
			if _, ok := dropIn[key]; ok {
				return fmt.Errorf("kubelet drop-in %s sets %s; configure the CPU manager with kubernetes.kubelet.cpuManager in the KubeSolo config instead", path, key)
			}
		}

		for _, key := range kubesoloDropInKeys {
			if _, ok := dropIn[key]; ok {
				log.Warn().Str("component", "kubelet").Msgf("kubelet drop-in %s overrides %s, which KubeSolo sets itself; the node may stop working", path, key)
			}
		}
		if s.containerMode {
			for _, key := range containerModeDropInKeys {
				if _, ok := dropIn[key]; ok {
					log.Warn().Str("component", "kubelet").Msgf("kubelet drop-in %s overrides %s, which KubeSolo turns off in container mode; the node may stop working", path, key)
				}
			}
		}

		log.Info().Str("component", "kubelet").Msgf("applying kubelet drop-in %s", path)
		return nil
	})
}
