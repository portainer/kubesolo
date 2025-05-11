package types

import "path/filepath"

var (
	KubesoloContainerdSockFile     = filepath.Join(DefaultContainerdDir, DefaultContainerdSocket)
	KubesoloContainerdStateDir     = filepath.Join(DefaultContainerdDir, "state")
	KubesoloContainerdRootDir      = filepath.Join(DefaultContainerdDir, "root")
	KubesoloContainerdCniDir       = filepath.Join(DefaultContainerdDir, "cni")
	KubesoloContainerdCniPluginDir = filepath.Join(KubesoloContainerdCniDir, "bin")
	KubesoloContainerdCniConfDir   = filepath.Join(KubesoloContainerdCniDir, "conf")
	KubesoloRuncDir                = filepath.Join(DefaultContainerdDir, "runc")
	KubesoloKubeletConfigDir       = filepath.Join(DefaultKubeletDir, "config")
	KubesoloKubeletVolumePluginDir = filepath.Join(DefaultKubeletDir, "volumeplugins")
	KubesoloKineDir                = filepath.Join(DefaultKineDir, "db")
	KubesoloKineSocketFile         = filepath.Join(DefaultKineDir, DefaultKineSocket)
	KubesoloControllerManagerDir   = filepath.Join(DefaultControllerManagerDir, "config")
	KubesoloWebhookDir             = filepath.Join(DefaultPKIDir, "webhook")
	KubesoloRequiredCNIPlugins     = []string{"bridge", "host-local", "portmap", "loopback"}
)
