// Package config derives KubeSolo's runtime configuration.
//
// BuildEmbedded turns the resolved settings into the types.Embedded struct that
// every service is constructed from. It is deliberately free of side effects —
// no host probing, no filesystem writes, no process exit — so that the mapping
// from settings to paths can be tested directly. Host probing (node IP, MTU,
// container detection) happens in the caller and arrives here as Input.
package config

import (
	"path/filepath"
	"strings"

	"github.com/portainer/kubesolo/internal/runtime/cri"
	"github.com/portainer/kubesolo/types"
)

// Probe carries what the host reported and BuildEmbedded cannot work out for
// itself. Everything else it needs comes from the configuration.
type Probe struct {
	// Hostname is the host's name, the fallback for kubernetes.nodeName.
	Hostname string

	NodeIP         string
	NodeIPPinned   bool
	LoadBalancerIP string
	MTU            int
	MTUPinned      bool
	ContainerMode  bool

	// RuntimeEndpoint is the endpoint parsed from runtime.endpoint. The zero
	// value means KubeSolo runs its own containerd, in which case BuildEmbedded
	// substitutes the embedded endpoint under the configured path.
	RuntimeEndpoint cri.Endpoint
}

// BuildEmbedded derives every path and setting KubeSolo runs with.
func BuildEmbedded(cfg *types.Config, probe Probe) types.Embedded {
	// Container runtime endpoint. Without --container-runtime-endpoint KubeSolo
	// starts its own containerd and talks to it over the socket under --path.
	containerdSocketFile := filepath.Join(cfg.Path, types.DefaultContainerdDir, types.DefaultContainerdSocket)
	runtimeEndpoint := probe.RuntimeEndpoint
	if !runtimeEndpoint.External {
		runtimeEndpoint = cri.Embedded(containerdSocketFile)
	}

	// The Kubernetes root CA. Supplied material is used where it lies rather than
	// copied in: it is commonly read-only and owned by whatever provisioned it,
	// and keeping it outside PKIDir puts it out of reach of removeLeafCerts,
	// which wipes everything under that directory when the node IP moves.
	caCert := filepath.Join(cfg.Path, types.DefaultPKIDir, "ca", "ca.crt")
	caKey := filepath.Join(cfg.Path, types.DefaultPKIDir, "ca", "ca.key")
	externalCA := cfg.PKI.CACert != "" && cfg.PKI.CAKey != ""
	if externalCA {
		caCert = cfg.PKI.CACert
		caKey = cfg.PKI.CAKey
	}

	// The node this control plane manages, defaulting to the hostname.
	//
	// Trimmed and lowercased to match what the kubelet does to --hostname-override
	// before registering. Without this a configured "Talos-CP-1" would register a
	// node called "talos-cp-1" while the NodeSetter webhook and the kubelet's
	// certificate subject kept the original spelling, and every pod would be
	// pinned to a node that does not exist. probe.Hostname is already normalised
	// by system.GetHostname.
	nodeName := strings.ToLower(strings.TrimSpace(cfg.Kubernetes.NodeName))
	if nodeName == "" {
		nodeName = probe.Hostname
	}

	return types.Embedded{
		NodeName:       nodeName,
		BootstrapToken: cfg.Kubernetes.BootstrapToken,
		ExternalCA:     externalCA,

		// System Node IP
		NodeIP:          probe.NodeIP,
		NodeIPSpecified: probe.NodeIPPinned,

		// Network MTU
		MTU:          probe.MTU,
		MTUSpecified: probe.MTUPinned,

		// Admin kubeconfig file
		AdminKubeconfigFile: filepath.Join(cfg.Path, types.DefaultPKIDir, "admin", "admin.kubeconfig"),

		// PKI paths
		PKIDir:              filepath.Join(cfg.Path, types.DefaultPKIDir),
		PKICADir:            filepath.Join(cfg.Path, types.DefaultPKIDir, "ca"),
		PKIAdminDir:         filepath.Join(cfg.Path, types.DefaultPKIDir, "admin"),
		PKIAPIServerDir:     filepath.Join(cfg.Path, types.DefaultPKIDir, "apiserver"),
		PKIControllerDir:    filepath.Join(cfg.Path, types.DefaultPKIDir, "controller-manager"),
		PKIKubeletDir:       filepath.Join(cfg.Path, types.DefaultPKIDir, "kubelet"),
		PKIWebhookDir:       filepath.Join(cfg.Path, types.DefaultPKIDir, "webhook"),
		PKIRequestHeaderDir: filepath.Join(cfg.Path, types.DefaultPKIDir, "request-header"),

		// Certificate paths
		KubeletCerts: types.KubeletCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: caCert,
				Cert:   filepath.Join(cfg.Path, types.DefaultPKIDir, "kubelet", "kubelet.crt"),
				Key:    filepath.Join(cfg.Path, types.DefaultPKIDir, "kubelet", "kubelet.key"),
			},
		},
		APIServerCerts: types.APIServerCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: caCert,
				Cert:   filepath.Join(cfg.Path, types.DefaultPKIDir, "apiserver", "apiserver.crt"),
				Key:    filepath.Join(cfg.Path, types.DefaultPKIDir, "apiserver", "apiserver.key"),
			},
		},
		ControllerManagerCerts: types.ControllerManagerCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: caCert,
				Cert:   filepath.Join(cfg.Path, types.DefaultPKIDir, "controller-manager", "controller-manager.crt"),
				Key:    filepath.Join(cfg.Path, types.DefaultPKIDir, "controller-manager", "controller-manager.key"),
			},
		},
		AdminCerts: types.AdminCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: caCert,
				Cert:   filepath.Join(cfg.Path, types.DefaultPKIDir, "admin", "admin.crt"),
				Key:    filepath.Join(cfg.Path, types.DefaultPKIDir, "admin", "admin.key"),
			},
		},
		WebhookCerts: types.WebhookCertificatePaths{
			CertificatePaths: types.CertificatePaths{
				CACert: caCert,
				Cert:   filepath.Join(cfg.Path, types.DefaultPKIDir, "webhook", "webhook.crt"),
				Key:    filepath.Join(cfg.Path, types.DefaultPKIDir, "webhook", "webhook.key"),
			},
		},
		CACerts: types.CACertificatePaths{
			Cert: caCert,
			Key:  caKey,
		},
		RequestHeaderCerts: types.RequestHeaderCertificatePaths{
			CACert:     filepath.Join(cfg.Path, types.DefaultPKIDir, "request-header", "request-header-ca.crt"),
			CAKey:      filepath.Join(cfg.Path, types.DefaultPKIDir, "request-header", "request-header-ca.key"),
			ClientCert: filepath.Join(cfg.Path, types.DefaultPKIDir, "request-header", "request-header-client.crt"),
			ClientKey:  filepath.Join(cfg.Path, types.DefaultPKIDir, "request-header", "request-header-client.key"),
		},

		// Containerd paths
		ContainerdDir:               filepath.Join(cfg.Path, types.DefaultContainerdDir),
		ContainerdSocketFile:        containerdSocketFile,
		ContainerdBinaryFile:        filepath.Join(cfg.Path, types.DefaultContainerdDir, "containerd"),
		ContainerdImagesDir:         filepath.Join(cfg.Path, types.DefaultContainerdDir, "images"),
		ContainerdShimBinaryFile:    filepath.Join(cfg.Path, types.DefaultContainerdDir, "containerd-shim-runc-v2"),
		ContainerdConfigFile:        filepath.Join(cfg.Path, types.DefaultContainerdDir, "config.toml"),
		ContainerdRootDir:           filepath.Join(cfg.Path, types.DefaultContainerdDir, "root"),
		ContainerdStateDir:          filepath.Join(cfg.Path, types.DefaultContainerdDir, "state"),
		ContainerdRegistryConfigDir: filepath.Join(cfg.Path, types.DefaultContainerdDir, "registry"),

		// CNI paths
		ContainerdCNIDir:        filepath.Join(cfg.Path, types.DefaultContainerdDir, "cni"),
		ContainerdCNIPluginsDir: filepath.Join(cfg.Path, types.DefaultContainerdDir, "cni", "plugins"),
		ContainerdCNIConfigDir:  filepath.Join(cfg.Path, types.DefaultContainerdDir, "cni", "conf"),
		ContainerdCNIConfigFile: filepath.Join(cfg.Path, types.DefaultContainerdDir, "cni", "conf", types.DefaultCNIConfigName),

		// Crun binary
		CrunBinaryFile: filepath.Join(cfg.Path, types.DefaultContainerdDir, "crun"),

		// Container runtime
		RuntimeExternal:   runtimeEndpoint.External,
		RuntimeEndpoint:   runtimeEndpoint.URL,
		RuntimeSocketPath: runtimeEndpoint.SocketPath,

		// Kubelet
		KubeletExternal:       cfg.Kubernetes.Kubelet.External,
		KubeletDir:            filepath.Join(cfg.Path, types.DefaultKubeletDir),
		KubeletConfigDir:      filepath.Join(cfg.Path, types.DefaultKubeletDir, "config"),
		KubeletConfigFile:     filepath.Join(cfg.Path, types.DefaultKubeletDir, "config", "config.yaml"),
		KubeletKubeConfigFile: filepath.Join(cfg.Path, types.DefaultPKIDir, "kubelet", "kubelet.kubeconfig"),
		KubeletPluginsDir:     filepath.Join(cfg.Path, types.DefaultKubeletDir, "volumeplugins"),

		// API Server paths
		APIServerDir:          filepath.Join(cfg.Path, types.DefaultAPIServerDir),
		ServiceAccountKeyFile: filepath.Join(cfg.Path, types.DefaultPKIDir, "apiserver", "service-account.key"),
		// API Server extra SANs
		APIServerExtraSANs: cfg.Kubernetes.APIServer.ExtraSANs,

		// Kine paths
		KineDir:        filepath.Join(cfg.Path, types.KubesoloKineDir),
		KineSocketFile: filepath.Join(cfg.Path, types.KubesoloKineDir, "socket"),

		// Controller manager paths
		ControllerDir: filepath.Join(cfg.Path, types.KubesoloControllerManagerDir),

		// Webhook paths
		WebhookDir: filepath.Join(cfg.Path, types.KubesoloWebhookDir),

		// Image paths
		PortainerEdgeImageFile:        filepath.Join(cfg.Path, types.DefaultContainerdDir, "images", "portainer-agent.tar.gz"),
		CorednsImageFile:              filepath.Join(cfg.Path, types.DefaultContainerdDir, "images", "coredns.tar.gz"),
		SandboxImageFile:              filepath.Join(cfg.Path, types.DefaultContainerdDir, "images", "pause.tar.gz"),
		LocalPathProvisionerImageFile: filepath.Join(cfg.Path, types.DefaultContainerdDir, "images", "local-path-provisioner.tar.gz"),

		// Load Balancer
		LoadBalancer:   cfg.Network.LoadBalancer.Enabled,
		LoadBalancerIP: probe.LoadBalancerIP,

		// Local Path Storage
		LocalPathStorageDir: filepath.Join(cfg.Path, types.DefaultLocalPathStorageDir),

		// Portainer Edge
		IsPortainerEdge:    cfg.Portainer.EdgeID != "" && cfg.Portainer.EdgeKey != "",
		PortainerEdgeImage: cfg.Portainer.Image,

		// Container Mode
		ContainerMode: probe.ContainerMode,

		// IPv6
		DisableIPv6: cfg.Network.DisableIPv6,

		// d2k integration
		D2K:          cfg.D2K.Enabled,
		D2KNamespace: cfg.D2K.Namespace,
		D2KCerts: types.D2KCertificatePaths{
			CACert:     caCert,
			ServerCert: filepath.Join(cfg.Path, types.DefaultPKIDir, types.DefaultD2KDir, "server.crt"),
			ServerKey:  filepath.Join(cfg.Path, types.DefaultPKIDir, types.DefaultD2KDir, "server.key"),
			ClientCert: filepath.Join(cfg.Path, types.DefaultPKIDir, types.DefaultD2KDir, "client.crt"),
			ClientKey:  filepath.Join(cfg.Path, types.DefaultPKIDir, types.DefaultD2KDir, "client.key"),
		},
		D2KImageFile: filepath.Join(cfg.Path, types.DefaultContainerdDir, "images", "d2k.tar.gz"),

		// CPU manager
		CPUManager:     cfg.Kubernetes.Kubelet.CPUManager,
		SystemReserved: cfg.Kubernetes.Kubelet.SystemReserved,

		// Metrics endpoint
		Metrics: cfg.Metrics,
	}
}
