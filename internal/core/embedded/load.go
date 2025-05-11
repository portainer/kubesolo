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
		{containerdBinary, embedded.ContainerdBinaryFile, "containerd"},
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
	return nil
}

// loadCNIPlugins extracts and installs CNI plugins
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

// loadCNIConfig creates the default CNI configuration file and symlinks it.
func loadCNIConfig(containerdCNIConfigDir, containerdCNIConfigFile string) error {
	if err := filesystem.EnsureDirectoryExists(containerdCNIConfigDir); err != nil {
		return fmt.Errorf("failed to create directory %s... %w", containerdCNIConfigDir, err)
	}

	if err := filesystem.EnsureDirectoryExists(types.DefaultStandardCNIConfDir); err != nil {
		return fmt.Errorf("failed to create target CNI config directory %s... %v", types.DefaultStandardCNIConfDir, err)
	}

	if err := filesystem.EnsureDirectoryExists(containerdCNIConfigDir); err != nil {
		return fmt.Errorf("failed to create target CNI config directory %s... %v", containerdCNIConfigDir, err)
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

// generateCNIConfigFile generates the default CNI configuration file
func generateCNIConfigFile() map[string]any {
	return map[string]any{
		"cniVersion": "1.0.0",
		"name":       "kubesolo-net",
		"plugins": []map[string]any{
			{
				"type":        "bridge",
				"bridge":      "cni0",
				"isGateway":   true,
				"ipMasq":      true,
				"hairpinMode": true,
				"capabilities": map[string]any{
					"portMappings": true,
					"ips":          true,
				},
				"ipam": map[string]any{
					"type": "host-local",
					"ranges": [][]map[string]any{
						{
							{
								"subnet": types.DefaultPodCIDR,
							},
						},
					},
					"routes": []map[string]any{
						{
							"dst": "0.0.0.0/0",
						},
					},
				},
			},
			{
				"type": "portmap",
				"capabilities": map[string]any{
					"portMappings": true,
				},
			},
			{
				"type": "loopback",
			},
		},
	}
}

// loadKernelModules loads necessary kernel modules
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

	rpFilterCmd := exec.Command("sysctl", "-w", "net.ipv4.conf.all.rp_filter=2")
	if err := rpFilterCmd.Run(); err != nil {
		log.Warn().Str("component", "embedded").Msgf("failed to set rp_filter... %v", err)
	}

	conntrackMaxCmd := exec.Command("sysctl", "-w", "net.netfilter.nf_conntrack_max=1000000")
	if err := conntrackMaxCmd.Run(); err != nil {
		log.Warn().Str("component", "embedded").Msgf("failed to set nf_conntrack_max... %v", err)
	}
	return nil
}

// loadImages loads the images into the containerd registry
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
	}

	for _, image := range images {
		if err := os.WriteFile(image.destination, image.source, 0644); err != nil {
			return fmt.Errorf("failed to write %s %s... %v", "cni plugins", image.name, err)
		}
	}
	return nil
}
