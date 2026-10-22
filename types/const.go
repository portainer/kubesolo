package types

import "time"

const (
	DefaultNodeName                  = "kubesolo-node"
	DefaultWebhookName               = "webhook.kubesolo.io"
	DefaultSystemCNIDir              = "/opt/cni"
	DefaultEmbeddedCNIDir            = "bin/cni"
	DefaultWebhookPort               = 10443
	DefaultAPIServerPort             = 6443
	DefaultControllerManagerPort     = 10257
	DefaultKubeletPort               = 10250
	DefaultKubeletHealthzPort        = 10248
	DefaultKubeProxyHealthzPort      = 10256
	DefaultKinePort                  = 2379
	DefaultPprofPort                 = 6060
	DefaultPKIDir                    = "pki"
	DefaultContainerdDir             = "containerd"
	DefaultContainerdSocket          = "containerd.sock"
	DefaultSystemContainerdSock      = "/run/containerd/containerd.sock"
	DefaultStandardCNIConfDir        = "/etc/cni/net.d"
	DefaultContainerdConfigDir       = "/etc/containerd/conf.d"
	LegacyContainerdConfigDir        = "/etc/containerd/config.d"
	DefaultCNIConfigName             = "10-bridge.conflist"
	DefaultK8sNamespace              = "k8s.io"
	DefaultKubeletDir                = "kubelet"
	DefaultAPIServerDir              = "apiserver"
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

	// DefaultAssistantContextSize fits a knowledge file, an evidence snapshot
	// and an answer, while keeping the assistant's memory near 2 GB.
	DefaultAssistantContextSize = 8192

	// AssistantMinCPUs and AssistantMinMemory are the smallest host the
	// assistant runs on. A host sold as 4 GB reports a little less, hence 3.5 GiB.
	AssistantMinCPUs   = 2
	AssistantMinMemory = 3584 << 20

	// DefaultAssistantDir holds the extracted llama-server, relative to the
	// base path.
	DefaultAssistantDir = "assistant"

	// DefaultAssistantModel is the model artifact used when assistant.model is
	// empty, pinned by digest so that a restart cannot change it. Its single
	// layer is the GGUF file; it is published by the assistant-model workflow.
	DefaultAssistantModel = "docker.io/portainerci/kubesolo-assistant-model:qwen3.5-2b-q4_k_m@sha256:77ff513db5cb970f52fabf6ea56e63bb649b409c303f8f06662da4b4c7aa0a83"

	// DefaultAssistantKnowledge is the knowledge artifact used when
	// assistant.knowledge is empty, pinned by digest. Its single layer is the
	// Markdown file build/assistant/knowledge.md; it is published by the
	// assistant-knowledge workflow.
	DefaultAssistantKnowledge = "docker.io/portainerci/kubesolo-assistant-knowledge:0.1.0@sha256:0be9480383a295363ae9a9a4abaf131d898f9794219c6f44452342f7f0d6a1a6"

	// DefaultAssistantStartTimeout bounds loading the model, which on slow
	// storage such as an SD card takes minutes.
	DefaultAssistantStartTimeout = 5 * time.Minute
)

// CPU manager policies. With the static policy, Guaranteed-QoS pods that request
// whole CPUs get exclusive cores instead of sharing the CFS quota pool.
const (
	CPUManagerPolicyNone   = "none"
	CPUManagerPolicyStatic = "static"
)
