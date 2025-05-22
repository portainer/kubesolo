package types

import "time"

const (
	DefaultNodeName                       = "kubesolo-node"
	DefaultWebhookName                    = "webhook.kubesolo.io"
	DefaultSystemCNIDir                   = "/opt/cni"
	DefaultEmbeddedCNIDir                 = "bin/cni"
	DefaultWebhookPort                    = 10443
	DefaultPKIDir                         = "pki"
	DefaultContainerdDir                  = "containerd"
	DefaultContainerdSocket               = "containerd.sock"
	DefaultSystemContainerdSock           = "/run/containerd/containerd.sock"
	DefaultStandardCNIBinDir              = "/opt/cni/bin"
	DefaultStandardCNIConfDir             = "/etc/cni/net.d"
	DefaultStandardRuncFile               = "/usr/local/bin/runc"
	DefaultStandardContainerdShimRuncFile = "/usr/local/bin/containerd-shim-runc-v2"
	DefaultCNIConfigName                  = "10-bridge.conflist"
	DefaultK8sNamespace                   = "k8s.io"
	DefaultKubeletDir                     = "kubelet"
	DefaultAPIServerDir                   = "apiserver"
	DefaultAPIServerAddress               = "https://127.0.0.1:6443"
	DefaultKineEndpoint                   = "127.0.0.1:2379"
	DefaultPodCIDR                        = "10.42.0.0/16"
	DefaultServiceClusterIPRange          = "10.43.0.0/16"
	DefaultCoreDNSIP                      = "10.43.0.10"
	DefaultKubernetesServiceIP            = "10.43.0.1"
	DefaultKineDir                        = "kine"
	DefaultKineSocket                     = "kine.sock"
	DefaultControllerManagerDir           = "controller-manager"
	DefaultSandboxImage                   = "registry.k8s.io/pause:3.10"
	DefaultPortainerAgentImage            = "portainer/agent:2.29.2"
	DefaultCoreDNSImage                   = "coredns/coredns:1.12.1"
	DefaultLocalPathProvisionerImage      = "rancher/local-path-provisioner:v0.0.31"
	DefaultGCPercent                      = 30
	DefaultContextTimeout                 = 15 * time.Second
	DefaultMemoryLimit                    = 75 * 1024 * 1024
	DefaultComponentSleep                 = 5 * time.Second
	DefaultRetryCount                     = 12
)
