package containerd

import (
	"os"

	"github.com/pelletier/go-toml"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

// isCgroupV2 returns true if the host uses the cgroupv2 unified hierarchy.
// When true, crun must use the systemd cgroup driver (SystemdCgroup=true)
// instead of the cgroupfs driver which generates cgroupv1-style paths.
func isCgroupV2() bool {
	_, err := os.Stat("/sys/fs/cgroup/cgroup.controllers")
	return err == nil
}

// isSystemdRunning returns true if systemd is the active init system.
// On non-systemd hosts (e.g. Alpine with OpenRC), the systemd cgroup driver
// must not be used even when cgroupv2 is available.
func isSystemdRunning() bool {
	_, err := os.Stat("/run/systemd/private")
	return err == nil
}

// useSystemdCgroup returns true only when both cgroupv2 and systemd are present.
func useSystemdCgroup() bool {
	return isCgroupV2() && isSystemdRunning()
}

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
		"version": 3,
		"root":    s.containerdRootDir,
		"state":   s.containerdStateDir,
		"imports": []string{types.DefaultContainerdConfigDir + "/*.toml"},
		"grpc": map[string]any{
			"address": s.containerdSocketFile,
		},

		"plugins": map[string]any{
			"io.containerd.cri.v1.images": s.generateCRIImagesConfig(),

			"io.containerd.cri.v1.runtime": map[string]any{
				"containerd": map[string]any{
					"default_runtime_name": "crun",
					"runtimes": map[string]any{
						"crun": map[string]any{
							"runtime_type": "io.containerd.runc.v2",
							"runtime_path": s.containerdShimBinaryFile,
							"options": map[string]any{
								"BinaryName":    s.crunBinaryFile,
								"SystemdCgroup": useSystemdCgroup(),
							},
						},
					},
				},
				"cni": map[string]any{
					"bin_dirs": []string{s.containerdCNIPluginsDir},
					"conf_dir": types.DefaultStandardCNIConfDir,
				},
			},

			"io.containerd.gc.v1.scheduler": s.generateGCSchedulerConfig(),

			"io.containerd.runtime.v2.task": map[string]any{
				"platforms": []string{"linux/amd64", "linux/arm64", "linux/arm"},
			},
		},
	}
}

// generateCRIImagesConfig returns the CRI images plugin configuration.
// Edge-specific overrides (max_concurrent_downloads, stats_collect_period) are
// only applied when not in full mode.
func (s *service) generateCRIImagesConfig() map[string]any {
	cfg := map[string]any{
		"image_pull_progress_timeout": "2m0s",
		"pinned_images": map[string]any{
			"sandbox": types.DefaultSandboxImage,
		},
		"registry": map[string]any{
			"config_path": s.containerdRegistryConfigDir,
		},
	}

	if !s.fullMode {
		cfg["max_concurrent_downloads"] = 1
		cfg["stats_collect_period"] = 120
	}

	return cfg
}

// generateGCSchedulerConfig returns the GC scheduler plugin configuration.
// Edge-specific overrides are only applied when not in full mode.
func (s *service) generateGCSchedulerConfig() map[string]any {
	if s.fullMode {
		return map[string]any{}
	}

	return map[string]any{
		"pause_threshold":    0.01,
		"deletion_threshold": 0,
		"mutation_threshold": 50,
		"schedule_delay":     "5s",
		"startup_delay":      "200ms",
	}
}

func (s *service) generateCustomFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "Path to the configuration file",
			Value:   s.containerdConfigFile,
		},
		&cli.StringFlag{
			Name:    "log-level",
			Aliases: []string{"l"},
			Usage:   "Set the logging level [trace, debug, info, warn, error, fatal, panic]",
		},
		&cli.StringFlag{
			Name:    "address",
			Aliases: []string{"a"},
			Usage:   "Address for containerd's GRPC server",
		},
		&cli.StringFlag{
			Name:  "root",
			Usage: "containerd root directory",
		},
		&cli.StringFlag{
			Name:  "state",
			Usage: "containerd state directory",
		},
	}
}
