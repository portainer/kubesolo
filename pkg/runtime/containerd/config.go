package containerd

import (
	"os"

	"github.com/pelletier/go-toml"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// writeConfigFile writes the containerd config to a file
func (s *service) writeContainerdConfigFile() error {
	tree, err := toml.TreeFromMap(s.generateContainerdConfig())
	if err != nil {
		log.Error().Str("component", "containerd").Msgf("failed to create TOML tree: %v", err)
		return err
	}

	configFile, err := os.Create(s.containerdConfigFile)
	if err != nil {
		log.Error().Str("component", "containerd").Msgf("failed to create config file: %v", err)
		return err
	}
	defer configFile.Close()

	_, err = tree.WriteTo(configFile)
	if err != nil {
		log.Error().Str("component", "containerd").Msgf("failed to write TOML config: %v", err)
		return err
	}

	return nil
}

// generateConfig generates the containerd config
func (s *service) generateContainerdConfig() map[string]any {
	return map[string]any{
		"root":  s.containerdRootDir,
		"state": s.containerdStateDir,

		"grpc": map[string]any{
			"address": s.containerdSocketFile,
			"uid":     0,
			"gid":     0,
		},

		"plugins": map[string]any{
			"io.containerd.grpc.v1.cri": map[string]any{
				"sandbox_image":             types.DefaultSandboxImage,
				"enable_unprivileged_ports": true,
				"enable_unprivileged_icmp":  true,
				"stream_server_address":     "127.0.0.1",
				"stream_server_port":        "10010",

				"cni": map[string]any{
					"bin_dir":       types.DefaultStandardCNIBinDir,
					"conf_dir":      types.DefaultStandardCNIConfDir,
					"max_conf_num":  1,
					"conf_template": "",
				},

				"dns": map[string]any{
					"nameservers": []string{types.DefaultCoreDNSIP, "8.8.8.8", "8.8.4.4"},
					"searches":    []string{"default.svc.cluster.local", "svc.cluster.local", "cluster.local"},
					"options":     []string{"ndots:5"},
				},

				"containerd": map[string]any{
					"snapshotter":          "overlayfs",
					"default_runtime_name": "runc",
					"runtimes": map[string]any{
						"runc": map[string]any{
							"runtime_type": "io.containerd.runc.v2",
							"options": map[string]any{
								"BinaryName":    s.runcBinaryFile,
								"SystemdCgroup": false,
							},
						},
					},
				},
				"registry": map[string]any{
					"mirrors": map[string]any{
						"docker.io": map[string]any{
							"endpoint": []string{"https://docker.io"},
						},
					},
					"config_path": "",
				},
			},

			"io.containerd.snapshotter.v1.overlayfs": map[string]any{},
			"io.containerd.runtime.v2.task": map[string]any{
				"platforms": []string{"linux/amd64", "linux/arm64", "linux/arm"},
			},
		},
	}
}
