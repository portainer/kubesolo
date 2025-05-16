package containerd

import (
	"os"

	"github.com/pelletier/go-toml"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
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
		"version":          3,
		"root":             s.containerdRootDir,
		"state":            s.containerdStateDir,
		"temp":             "",
		"plugin_dir":       "",
		"disabled_plugins": []string{},
		"required_plugins": []string{},
		"oom_score":        0,
		"imports":          []string{},

		"grpc": map[string]any{
			"address": s.containerdSocketFile,
			"uid":     0,
			"gid":     0,
		},

		"plugins": map[string]any{
			"io.containerd.cri.v1.images": map[string]any{
				"snapshotter":                  "overlayfs",
				"disable_snapshot_annotations": true,
				"discard_unpacked_layers":      false,
				"max_concurrent_downloads":     3,
				"image_pull_progress_timeout":  "5m0s",
				"image_pull_with_sync_fs":      false,
				"stats_collect_period":         10,
				"pinned_images": map[string]any{
					"sandbox": types.DefaultSandboxImage,
				},
				"registry": map[string]any{
					"config_path": "",
				},
				"image_decryption": map[string]any{
					"key_model": "node",
				},
			},

			"io.containerd.cri.v1.runtime": map[string]any{
				"enable_selinux":                         false,
				"selinux_category_range":                 1024,
				"max_container_log_line_size":            16384,
				"disable_apparmor":                       false,
				"restrict_oom_score_adj":                 false,
				"disable_proc_mount":                     false,
				"unset_seccomp_profile":                  "",
				"tolerate_missing_hugetlb_controller":    true,
				"disable_hugetlb_controller":             true,
				"device_ownership_from_security_context": false,
				"ignore_image_defined_volumes":           false,
				"netns_mounts_under_state_dir":           false,
				"enable_unprivileged_ports":              true,
				"enable_unprivileged_icmp":               true,
				"enable_cdi":                             true,
				"drain_exec_sync_io_timeout":             "0s",
				"ignore_deprecation_warnings":            []string{},
				"containerd": map[string]any{
					"default_runtime_name":              "runc",
					"ignore_blockio_not_enabled_errors": false,
					"ignore_rdt_not_enabled_errors":     false,
					"runtimes": map[string]any{
						"runc": map[string]any{
							"runtime_type":                    "io.containerd.runc.v2",
							"runtime_path":                    "",
							"pod_annotations":                 []string{},
							"container_annotations":           []string{},
							"privileged_without_host_devices": false,
							"privileged_without_host_devices_all_devices_allowed": false,
							"base_runtime_spec": "",
							"cni_conf_dir":      "",
							"cni_max_conf_num":  0,
							"snapshotter":       "",
							"sandboxer":         "podsandbox",
							"io_type":           "",
							"options": map[string]any{
								"BinaryName": s.runcBinaryFile,
							},
						},
					},
				},
				"cni": map[string]any{
					"bin_dir":               types.DefaultStandardCNIBinDir,
					"conf_dir":              types.DefaultStandardCNIConfDir,
					"max_conf_num":          1,
					"setup_serially":        false,
					"conf_template":         "",
					"ip_pref":               "",
					"use_internal_loopback": false,
				},
			},

			"io.containerd.gc.v1.scheduler": map[string]any{
				"pause_threshold":    0.02,
				"deletion_threshold": 0,
				"mutation_threshold": 100,
				"schedule_delay":     "0s",
				"startup_delay":      "100ms",
			},

			"io.containerd.grpc.v1.cri": map[string]any{
				"disable_tcp_service":   true,
				"stream_server_address": "127.0.0.1",
				"stream_server_port":    "0",
				"stream_idle_timeout":   "4h0m0s",
				"enable_tls_streaming":  false,
			},

			"io.containerd.snapshotter.v1.overlayfs": map[string]any{
				"root_path":      "",
				"upperdir_label": false,
				"sync_remove":    false,
				"slow_chown":     false,
				"mount_options":  []string{},
			},

			"io.containerd.runtime.v2.task": map[string]any{
				"platforms": []string{"linux/amd64", "linux/arm64", "linux/arm"},
			},
		},

		"cgroup": map[string]any{
			"path": "",
		},

		"timeouts": map[string]any{
			"io.containerd.timeout.bolt.open":         "0s",
			"io.containerd.timeout.metrics.shimstats": "2s",
			"io.containerd.timeout.shim.cleanup":      "5s",
			"io.containerd.timeout.shim.load":         "5s",
			"io.containerd.timeout.shim.shutdown":     "3s",
			"io.containerd.timeout.task.state":        "2s",
		},
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
