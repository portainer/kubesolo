package types

import "path/filepath"

var (
	KubesoloKineDir              = filepath.Join(DefaultKineDir, "db")
	KubesoloControllerManagerDir = filepath.Join(DefaultControllerManagerDir, "config")
	KubesoloWebhookDir           = filepath.Join(DefaultPKIDir, "webhook")
	KubesoloRequiredCNIPlugins   = []string{"bridge", "host-local", "portmap", "loopback"}
)
