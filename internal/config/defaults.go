package config

import "github.com/portainer/kubesolo/types"

// Defaults returns the configuration KubeSolo runs with when nothing is set.
//
// This is the single source of the default values. The kingpin flags carry no
// defaults of their own: a flag that declared one would be indistinguishable
// from a flag the user set, and the config file could then never win over it.
func Defaults() *types.Config {
	return &types.Config{
		APIVersion: types.ConfigAPIVersion,
		Kind:       types.ConfigKind,

		Path: types.DefaultBasePath,

		PKI: types.PKIConfig{
			CACert: "", // KubeSolo generates and owns the CA
			CAKey:  "",
		},

		Logging: types.LoggingConfig{
			Debug: false,
			Pprof: false,
		},

		Network: types.NetworkConfig{
			NodeIP:      "", // auto-detect
			MTU:         0,  // auto-detect
			DisableIPv6: false,
			LoadBalancer: types.LoadBalancerConfig{
				Enabled: true,
				IP:      "", // follow the node IP
			},
		},

		Runtime: types.RuntimeConfig{
			Endpoint:      "",  // run our own containerd
			ContainerMode: nil, // auto-detect
		},

		Kubernetes: types.KubernetesConfig{
			NodeName:       "", // the hostname
			BootstrapToken: "", // TLS bootstrapping off
			APIServer: types.APIServerConfig{
				ExtraSANs:             nil,
				StartupTimeoutSeconds: types.DefaultStartupTimeout,
			},
			Kubelet: types.KubeletConfig{
				CPUManager: types.CPUManagerConfig{
					Policy:        types.CPUManagerPolicyNone,
					PolicyOptions: nil,
					ReservedCPUs:  "",
				},
				SystemReserved: nil,
			},
		},

		Storage: types.StorageConfig{
			LocalPath: types.LocalPathConfig{
				Enabled:    true,
				SharedPath: "",
			},
			DBWALRepair: false,
		},

		Portainer: types.PortainerConfig{
			EdgeID:  "",
			EdgeKey: "",
			Async:   false,
			Image:   types.DefaultPortainerEdgeImage,
		},

		D2K: types.D2KConfig{
			Enabled:   false,
			Namespace: types.DefaultD2KNamespace,
		},

		Metrics: types.MetricsConfig{
			Enabled:     false,
			BindAddress: types.DefaultMetricsBindAddress,
		},

		API: types.ConfigAPI{
			Enabled:    false,
			SocketPath: "", // derived from Path
		},
	}
}
