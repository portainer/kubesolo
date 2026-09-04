package types

import "time"

const (
	DefaultNodeName                  = "kubesolo-node"
	DefaultWebhookName               = "webhook.kubesolo.io"
	DefaultSystemCNIDir              = "/opt/cni"
	DefaultEmbeddedCNIDir            = "bin/cni"
	DefaultWebhookPort               = 10443
	DefaultPKIDir                    = "pki"
	DefaultContainerdDir             = "containerd"
	DefaultContainerdSocket          = "containerd.sock"
	DefaultSystemContainerdSock      = "/run/containerd/containerd.sock"
	DefaultStandardCNIConfDir        = "/etc/cni/net.d"
	DefaultContainerdConfigDir       = "/etc/containerd/config.d"
	DefaultCNIConfigName             = "10-bridge.conflist"
	DefaultK8sNamespace              = "k8s.io"
	DefaultKubeletDir                = "kubelet"
	DefaultAPIServerDir              = "apiserver"
	DefaultKineEndpoint              = "127.0.0.1:2379"
	DefaultPodCIDR                   = "10.42.0.0/16"
	DefaultServiceClusterIPRange     = "10.43.0.0/16"
	DefaultCoreDNSIP                 = "10.43.0.10"
	DefaultKubernetesServiceIP       = "10.43.0.1"
	DefaultKineDir                   = "kine"
	DefaultKineSocket                = "kine.sock"
	DefaultControllerManagerDir      = "controller-manager"
	DefaultSandboxImage              = "docker.io/portainer/pause:latest"
	DefaultPortainerEdgeImage        = "docker.io/portainer/agent:lts"
	DefaultCoreDNSImage              = "docker.io/coredns/coredns:1.14.4"
	DefaultLocalPathProvisionerImage = "docker.io/rancher/local-path-provisioner:v0.0.36"
	DefaultLocalPathStorageDir       = "local-path-storage"
	DefaultD2KImage                  = "docker.io/portainer/d2k:1.2.3"
	DefaultD2KPort                   = int32(2376)
	DefaultD2KDir                    = "d2k"
	DefaultWebhookReadWriteTimeout   = 10 * time.Second
	DefaultWebhookIdleTimeout        = 30 * time.Second
	DefaultContextTimeout            = 60 * time.Second
	DefaultComponentSleep            = 5 * time.Second
	DefaultStartupTimeout            = 600 // seconds
	DefaultNftMasqTable              = "kubesolo-masq"
	DefaultMTU                       = 1500 // fallback when MTU detection fails
	DefaultMetricsBindAddress        = "127.0.0.1:9105"
	DefaultMetricsProbeInterval      = 15 * time.Second
	DefaultKineDBFile                = "state.db"

	// DefaultBasePath is the directory KubeSolo stores all of its state in, and
	// the default for --path. Mirrored by internal/cli/config.DefaultPath.
	DefaultBasePath = "/var/lib/kubesolo"

	// DefaultConfigFile is where KubeSolo reads its configuration from. It sits
	// outside DefaultBasePath deliberately: configuration is not cluster state, so
	// it must survive `kubesoloctl reset`, which removes the data directory.
	DefaultConfigFile = "/etc/kubesolo/config.yaml"

	// DefaultAPISocketName is the unix socket the config API listens on, relative
	// to the base path.
	DefaultAPISocketName = "config.sock"

	// DefaultD2KNamespace is the namespace d2k is deployed into. Distinct from
	// DefaultD2KDir, which names a PKI subdirectory that happens to match.
	DefaultD2KNamespace = "d2k"
)

// CPU manager policies. With the static policy, Guaranteed-QoS pods that request
// whole CPUs get exclusive cores instead of sharing the CFS quota pool.
const (
	CPUManagerPolicyNone   = "none"
	CPUManagerPolicyStatic = "static"
)
