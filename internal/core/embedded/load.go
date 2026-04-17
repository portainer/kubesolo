package embedded

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// loadContainerdComponents extracts and installs the containerd binary
func loadContainerdComponents(embedded types.Embedded) error {
	if err := filesystem.EnsureDirectoryExists(embedded.ContainerdDir); err != nil {
		return fmt.Errorf("failed to create directory %s... %w", embedded.ContainerdDir, err)
	}

	binaries := []struct {
		source      []byte
		destination string
		name        string
	}{
		{containerdShimBinary, embedded.ContainerdShimBinaryFile, "containerd-shim-runc-v2"},
		{crunBinary, embedded.CrunBinaryFile, "crun"},
	}

	for _, binary := range binaries {
		if err := filesystem.ExtractBinary(binary.source, binary.destination); err != nil {
			return fmt.Errorf("failed to extract %s binary: %v", binary.name, err)
		}
	}
	return nil
}

// loadCNIPlugins creates the necessary directories and extracts and installs requried CNI plugins; "bridge", "host-local", "portmap", "loopback"
// it then creates a symlink to the standard CNI bin directory
func loadCNIPlugins(containerdCNIDir, containerdCNIPluginsDir string) error {
	dirs := []string{
		containerdCNIDir,
		containerdCNIPluginsDir,
	}

	for _, dir := range dirs {
		if err := filesystem.EnsureDirectoryExists(dir); err != nil {
			return fmt.Errorf("failed to create directory %s... %w", dir, err)
		}
	}

	plugins := []struct {
		source      []byte
		destination string
		name        string
	}{
		{cniPluginBridge, filepath.Join(containerdCNIPluginsDir, "bridge"), "bridge"},
		{cniPluginHostLocal, filepath.Join(containerdCNIPluginsDir, "host-local"), "host-local"},
		{cniPluginPortmap, filepath.Join(containerdCNIPluginsDir, "portmap"), "portmap"},
		{cniPluginLoopback, filepath.Join(containerdCNIPluginsDir, "loopback"), "loopback"},
	}

	for _, plugin := range plugins {
		if err := filesystem.ExtractBinary(plugin.source, plugin.destination); err != nil {
			return fmt.Errorf("failed to extract %s binary: %v", plugin.name, err)
		}
	}
	return nil
}

// loadCNIConfig creates the necessary directories, generates the default CNI configuration file and symlinks it
// to the standard CNI config directory
func loadCNIConfig(containerdCNIConfigDir, containerdCNIConfigFile string) error {
	dirs := []string{
		types.DefaultStandardCNIConfDir,
		containerdCNIConfigDir,
	}

	for _, dir := range dirs {
		if err := filesystem.EnsureDirectoryExists(dir); err != nil {
			return fmt.Errorf("failed to create target CNI config directory %s... %v", dir, err)
		}
	}

	cniConfig, err := json.Marshal(generateCNIConfigFile())
	if err != nil {
		log.Error().Str("component", "embedded").Msgf("failed to marshal cni config: %v", err)
		return err
	}

	if err := os.WriteFile(containerdCNIConfigFile, cniConfig, 0644); err != nil {
		return fmt.Errorf("failed to write cni default config to %s... %v", containerdCNIConfigFile, err)
	}

	if err := filesystem.EnsureSymbolicLink(containerdCNIConfigFile, filepath.Join(types.DefaultStandardCNIConfDir, types.DefaultCNIConfigName)); err != nil {
		return fmt.Errorf("failed to create symlink for CNI config %s... %v", types.DefaultCNIConfigName, err)
	}

	return nil
}

// loadKernelModules loads the necessary kernel modules.
// Legacy ip_tables modules are attempted but failures are non-fatal since
// nova8OS uses nf_tables natively — iptables-nft works via nft_compat.
func loadKernelModules() error {
	essentialModules := []string{
		"overlay",
		"br_netfilter",
		"nf_conntrack",
	}

	for _, module := range essentialModules {
		command := exec.Command("modprobe", module)
		if err := command.Run(); err != nil {
			return fmt.Errorf("failed to load essential kernel module %s... %v", module, err)
		}
	}

	// Legacy iptables modules — soft-fail since nova8OS uses nf_tables backend
	optionalModules := []string{
		"ip_tables",
		"iptable_filter",
		"iptable_nat",
	}
	for _, module := range optionalModules {
		command := exec.Command("modprobe", module)
		if err := command.Run(); err != nil {
			log.Debug().Str("component", "embedded").Msgf("optional module %s not available (nf_tables backend used) — skipping", module)
		}
	}

	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644); err != nil {
		log.Debug().Str("component", "embedded").Msgf("Failed to enable IP forwarding... %v", err)
	}
	return nil
}

// loadImages loads the images; "portainer-agent", "coredns", "local-path-provisioner" and "pause" into the containerd images directory
func loadImages(containerdImagesDir string) error {
	if err := filesystem.EnsureDirectoryExists(containerdImagesDir); err != nil {
		return fmt.Errorf("failed to create directory %s... %w", containerdImagesDir, err)
	}

	images := []struct {
		source      []byte
		destination string
		name        string
	}{
		{portainerAgentImageFile, filepath.Join(containerdImagesDir, "portainer-agent.tar.gz"), "portainer-agent"},
		{corednsImageFile, filepath.Join(containerdImagesDir, "coredns.tar.gz"), "coredns"},
		{localPathProvisionerImageFile, filepath.Join(containerdImagesDir, "local-path-provisioner.tar.gz"), "local-path-provisioner"},
		{sandboxImageFile, filepath.Join(containerdImagesDir, "pause.tar.gz"), "pause"},
	}

	for _, image := range images {
		// Skip writing empty image data (e.g., portainer-agent on riscv64)
		if len(image.source) == 0 {
			log.Debug().Str("component", "embedded").Msgf("skipping empty image: %s", image.name)
			continue
		}

		if err := os.WriteFile(image.destination, image.source, 0644); err != nil {
			return fmt.Errorf("failed to write image %s... %v", image.name, err)
		}
	}
	return nil
}
