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
	DefaultPortainerAgentImage       = "docker.io/portainer/agent:2.39.2"
	DefaultCoreDNSImage              = "docker.io/coredns/coredns:1.14.3"
	DefaultLocalPathProvisionerImage = "docker.io/rancher/local-path-provisioner:v0.0.36"
	DefaultLocalPathStorageDir       = "local-path-storage"
	DefaultWebhookReadWriteTimeout   = 10 * time.Second
	DefaultWebhookIdleTimeout        = 30 * time.Second
	DefaultContextTimeout            = 60 * time.Second
	DefaultComponentSleep            = 5 * time.Second
	DefaultStartupTimeout            = 600 // seconds
	DefaultNftMasqTable              = "kubesolo-masq"
)
