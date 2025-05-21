package embedded

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

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
		{runcBinary, embedded.RuncBinaryFile, "runc"},
	}

	for _, binary := range binaries {
		if err := filesystem.ExtractBinary(binary.source, binary.destination); err != nil {
			return fmt.Errorf("failed to extract %s binary: %v", binary.name, err)
		}
	}

	if err := filesystem.EnsureSymbolicLink(embedded.RuncBinaryFile, types.DefaultStandardRuncFile); err != nil {
		return fmt.Errorf("failed to create symlink for runc: %v", err)
	}

	if err := filesystem.EnsureSymbolicLink(embedded.ContainerdShimBinaryFile, types.DefaultStandardContainerdShimRuncFile); err != nil {
		return fmt.Errorf("failed to create symlink for containerd-shim-runc-v2: %v", err)
	}
	return nil
}

// loadCNIPlugins creates the necessary directories and extracts and installs requried CNI plugins; "bridge", "host-local", "portmap", "loopback"
// it then creates a symlink to the standard CNI bin directory
func loadCNIPlugins(containerdCNIDir, containerdCNIPluginsDir string) error {
	entries, err := fs.ReadDir(cniPluginsFS, types.DefaultEmbeddedCNIDir)
	if err != nil {
		return fmt.Errorf("failed to read embedded cni plugins... %v", err)
	}

	dirs := []string{
		containerdCNIDir,
		containerdCNIPluginsDir,
		types.DefaultStandardCNIBinDir,
	}

	for _, dir := range dirs {
		if err := filesystem.EnsureDirectoryExists(dir); err != nil {
			return fmt.Errorf("failed to create directory %s... %w", dir, err)
		}
	}

	for _, entry := range entries {
		binaryName := entry.Name()
		if entry.IsDir() || !slices.Contains(types.KubesoloRequiredCNIPlugins, binaryName) {
			continue
		}

		srcFilePath := filepath.Join(types.DefaultEmbeddedCNIDir, binaryName)
		destFilePath := filepath.Join(containerdCNIPluginsDir, binaryName)

		if _, err := os.Stat(destFilePath); err == nil {
			continue
		}

		binaryData, err := cniPluginsFS.ReadFile(srcFilePath)
		if err != nil {
			return fmt.Errorf("failed to read embedded %s %s... %v", "cni plugins", binaryName, err)
		}

		if err := os.WriteFile(destFilePath, binaryData, 0755); err != nil {
			return fmt.Errorf("failed to write %s %s... %v", "cni plugins", binaryName, err)
		}
	}

	if err := filesystem.EnsureSymbolicLink(containerdCNIPluginsDir, types.DefaultStandardCNIBinDir); err != nil {
		return fmt.Errorf("failed to create symlink for cni plugins: %v", err)
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

// loadKernelModules loads the necessary kernel modules
// "overlay", "br_netfilter", "ip_tables", "iptable_filter", "iptable_nat", "nf_conntrack"
func loadKernelModules() error {
	essentialModules := []string{
		"overlay",
		"br_netfilter",
		"ip_tables",
		"iptable_filter",
		"iptable_nat",
		"nf_conntrack",
	}

	for _, module := range essentialModules {
		command := exec.Command("modprobe", module)
		if err := command.Run(); err != nil {
			return fmt.Errorf("failed to load essential kernel module %s... %v", module, err)
		}
	}

	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644); err != nil {
		log.Debug().Str("component", "embedded").Msgf("Failed to enable IP forwarding... %v", err)
	}
	return nil
}

// loadImages loads the images; "portainer-agent", "coredns" and "local-path-provisioner" into the containerd images directory
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
	}

	for _, image := range images {
		if err := os.WriteFile(image.destination, image.source, 0644); err != nil {
			return fmt.Errorf("failed to write %s %s... %v", "cni plugins", image.name, err)
		}
	}
	return nil
}
