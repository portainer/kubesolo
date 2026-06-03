package types

import "path/filepath"

var (
	KubesoloKineDir              = filepath.Join(DefaultKineDir, "db")
	KubesoloControllerManagerDir = filepath.Join(DefaultControllerManagerDir, "config")
	KubesoloWebhookDir           = filepath.Join(DefaultPKIDir, "webhook")

	// DefaultRetryCount is the number of health-check retries per component.
	// Derived from --startup-timeout at startup: timeout / DefaultComponentSleep.
	DefaultRetryCount = DefaultStartupTimeout / int(DefaultComponentSleep.Seconds())
)
