package cli

import (
	"runtime"
	"strings"

	"github.com/portainer/kubesolo/internal/cli/service"
)

// ensureArg returns args with flag appended if it is not already present (in
// either bare "--flag" or "--flag=value" form). Used to guarantee container-mode
// invariants such as --full survive an upgrade of a container created before
// they became the default, where the original CMD is reused verbatim.
func ensureArg(args []string, flag string) []string {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return args
		}
	}
	return append(args, flag)
}

// containerModeActive reports whether the named KubeSolo instance runs as a
// container on this host. It is true on macOS (the binary is Linux-only, so
// KubeSolo always runs in a container there) or when a KubeSolo container for
// the instance exists on any other host (Windows WSL2 or Linux).
//
// Lifecycle commands (reset, upgrade, uninstall, kubeconfig) use this to pick
// the container vs. host code path by what is actually running, rather than
// keying on the OS — container mode is supported wherever a container engine is.
func containerModeActive(instanceName string) bool {
	return runtime.GOOS == "darwin" || service.ContainerExists(instanceName)
}

// containerModeActiveFor is like containerModeActive but takes a fully-resolved
// container name (as the kubeconfig commands accept via --container) instead of
// an instance name.
func containerModeActiveFor(containerName string) bool {
	return runtime.GOOS == "darwin" || service.ContainerExistsByName(containerName)
}
