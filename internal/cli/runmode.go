package cli

import (
	"runtime"

	"github.com/portainer/kubesolo/internal/cli/service"
)

// containerModeActive reports whether the named KubeSolo instance runs as a
// container on this host. It is true on macOS (the binary is Linux-only, so
// KubeSolo always runs in a container there) or when the instance's container,
// network or data volume exists on any other host (Windows WSL2 or Linux).
//
// Lifecycle commands (reset, upgrade, uninstall, kubeconfig) use this to pick
// the container vs. host code path by what is actually running, rather than
// keying on the OS — container mode is supported wherever a container engine is.
func containerModeActive(instanceName string) bool {
	return runtime.GOOS == "darwin" || service.InstanceExists(instanceName)
}

// containerModeActiveFor is like containerModeActive but takes a fully-resolved
// container name (as the kubeconfig commands accept via --container) instead of
// an instance name.
func containerModeActiveFor(containerName string) bool {
	return runtime.GOOS == "darwin" || service.ContainerExistsByName(containerName)
}
