package system

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// IsRunningInContainer detects if the process is running inside a container
// by checking for common container indicators
func IsRunningInContainer() bool {
	// Check for Docker
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// Check for Podman
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}

	// Check for systemd container environment variable
	if os.Getenv("container") != "" {
		return true
	}

	return false
}

// SetupContainerMounts ensures mount propagation is set to rshared on the root
// filesystem. Without this, kubelet cannot propagate volume mounts (including
// projected service account tokens) into pod containers.
func SetupContainerMounts() error {
	if err := mountMakeRShared(); err != nil {
		return err
	}
	log.Info().Str("component", "mount").Msg("set root filesystem to rshared propagation")
	return nil
}

// SetupContainerCgroups prepares cgroup v2 for running nested containers.
// In cgroupv2, a cgroup cannot both contain processes AND have domain controllers
// delegated to child cgroups (the "no internal processes" rule).
// This function creates a child cgroup (/sys/fs/cgroup/init), moves the current
// process into it, and enables controller delegation on the root cgroup so that
// containerd/runc can create child cgroups (like /sys/fs/cgroup/k8s.io).
func SetupContainerCgroups() error {
	const cgroupRoot = "/sys/fs/cgroup"

	// Check if this is cgroupv2 by looking for cgroup.controllers
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err != nil {
		log.Debug().Str("component", "cgroup").Msg("not cgroupv2, skipping cgroup setup")
		return nil
	}

	// Create /sys/fs/cgroup/init if it doesn't exist
	initCgroup := filepath.Join(cgroupRoot, "init")
	if err := os.MkdirAll(initCgroup, 0o755); err != nil {
		return fmt.Errorf("failed to create init cgroup: %w", err)
	}

	// Move current process (PID 1) to the init cgroup
	pid := os.Getpid()
	procsFile := filepath.Join(initCgroup, "cgroup.procs")
	if err := os.WriteFile(procsFile, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("failed to move PID %d to init cgroup: %w", pid, err)
	}
	log.Info().Str("component", "cgroup").Int("pid", pid).Msg("moved process to /sys/fs/cgroup/init")

	// Read available controllers and enable them on the root cgroup's subtree_control
	controllersRaw, err := os.ReadFile(filepath.Join(cgroupRoot, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("failed to read cgroup controllers: %w", err)
	}

	controllers := strings.Fields(strings.TrimSpace(string(controllersRaw)))
	if len(controllers) > 0 {
		// Build "+cpu +memory +pids +io ..." string
		var enableList []string
		for _, c := range controllers {
			enableList = append(enableList, "+"+c)
		}
		subtreeControl := strings.Join(enableList, " ")

		if err := os.WriteFile(filepath.Join(cgroupRoot, "cgroup.subtree_control"), []byte(subtreeControl), 0o644); err != nil {
			log.Warn().Str("component", "cgroup").Err(err).Str("controllers", subtreeControl).Msg("failed to enable all controllers on subtree_control, trying one by one")
			// Try enabling controllers one by one - some may not be delegatable
			for _, entry := range enableList {
				if err := os.WriteFile(filepath.Join(cgroupRoot, "cgroup.subtree_control"), []byte(entry), 0o644); err != nil {
					log.Warn().Str("component", "cgroup").Err(err).Str("controller", entry).Msg("failed to enable controller")
				}
			}
		}
		log.Info().Str("component", "cgroup").Str("controllers", subtreeControl).Msg("enabled controller delegation on root cgroup")
	}

	return nil
}

// GetHostname returns the hostname of the machine
// it returns the hostname of the machine
// if it fails, it uses the default value "kubesolo-node"
func GetHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Warn().Str("component", "kubesolo").Msg("failed to get hostname, using default value")
		hostname = types.DefaultNodeName
	}
	return strings.ToLower(hostname)
}
