package embedded

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// requiredCNIPlugins are the plugins named by the CNI config kubesolo writes
var requiredCNIPlugins = []string{"bridge", "host-local", "portmap", "loopback"}

// cniConfigExtensions are the file extensions a container runtime loads from the
// CNI config directory
var cniConfigExtensions = []string{".conf", ".conflist", ".json"}

// ensureHostDependencies prepares the node for a container runtime the host manages.
//
// The host owns the runtime, the OCI runtime, the CNI plugin binaries and the sandbox
// image, so none of those are installed here. Two things stay kubesolo's: the kernel
// modules and sysctls Kubernetes networking needs, and the CNI config — its pod subnet
// has to match the cluster CIDR given to the controller manager and kube-proxy, its
// ipMasq has to stay off because kubesolo installs its own masquerade rules, and its
// MTU comes from --mtu.
func ensureHostDependencies(embedded types.Embedded) error {
	log.Info().Str("component", "embedded").Msg("container runtime is managed by the host, only kernel modules and the cni config are prepared by kubesolo...")

	if err := loadKernelModules(); err != nil {
		log.Warn().Str("component", "embedded").Msgf("failed to load kernel modules: %v", err)
	}

	if err := filesystem.EnsureDirectoryExists(types.DefaultStandardCNIConfDir); err != nil {
		return fmt.Errorf("failed to create target CNI config directory %s... %v", types.DefaultStandardCNIConfDir, err)
	}

	// Written straight into the standard directory rather than symlinked from the
	// kubesolo data directory: a symlink is opaque to an operator inspecting the host
	// with crictl, and it dangles once kubesolo is removed, which makes the runtime
	// log a CNI load failure for every sandbox it creates.
	cniConfigFile := filepath.Join(types.DefaultStandardCNIConfDir, types.DefaultCNIConfigName)
	if err := writeCNIConfigFile(cniConfigFile, embedded.MTU); err != nil {
		return fmt.Errorf("%v (the directory has to be writable, so on a read-only rootfs it must be mounted read-write)", err)
	}

	warnMissingCNIPlugins()
	logCNIConfigOrder(cniConfigFile)

	return nil
}

// warnMissingCNIPlugins reports plugins named by the CNI config that are absent from
// the standard plugin directory.
//
// This warns rather than fails: kubesolo cannot read the host runtime's configuration,
// so the runtime may well load its plugins from somewhere else and a hard failure on a
// guess would block a working host. Without the warning, the only symptom of a genuinely
// missing plugin is `plugin type="bridge" not found` when the runtime creates its first
// sandbox.
func warnMissingCNIPlugins() {
	pluginsDir := filepath.Join(types.DefaultSystemCNIDir, "bin")

	var missing []string
	for _, plugin := range requiredCNIPlugins {
		if _, err := os.Stat(filepath.Join(pluginsDir, plugin)); err != nil {
			missing = append(missing, plugin)
		}
	}

	if len(missing) == 0 {
		log.Debug().Str("component", "embedded").Msgf("all required cni plugins are present in %s", pluginsDir)
		return
	}

	log.Warn().Str("component", "embedded").Msgf("cni plugins %s were not found in %s... the host has to provide them, or the container runtime has to be configured to load them from elsewhere, otherwise pods will fail to start", strings.Join(missing, ", "), pluginsDir)
}

// logCNIConfigOrder reports the other CNI configurations sharing the directory.
//
// A container runtime loads the lexicographically first configuration it finds, so a
// file sorting ahead of kubesolo's takes over pod networking. That is exactly how a
// CNI such as Cilium is layered on top of kubesolo, which is why this warns instead
// of failing.
func logCNIConfigOrder(cniConfigFile string) {
	dir, name := filepath.Split(cniConfigFile)

	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Debug().Str("component", "embedded").Msgf("failed to read cni config directory %s: %v", dir, err)
		return
	}

	for _, entry := range entries {
		other := entry.Name()
		if other == name || !slices.Contains(cniConfigExtensions, filepath.Ext(other)) {
			continue
		}

		if other < name {
			log.Warn().Str("component", "embedded").Msgf("cni config %s sorts before %s and will be used instead... pods will join that network", other, name)
			continue
		}

		log.Info().Str("component", "embedded").Msgf("found additional cni config %s, which sorts after %s", other, name)
	}
}
