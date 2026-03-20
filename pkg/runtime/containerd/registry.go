package containerd

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// wellKnownUpstreamServers maps registry hostnames to their canonical upstream URLs.
var wellKnownUpstreamServers = map[string]string{
	"docker.io":       "https://registry-1.docker.io",
	"ghcr.io":         "https://ghcr.io",
	"gcr.io":          "https://gcr.io",
	"quay.io":         "https://quay.io",
	"registry.k8s.io": "https://registry.k8s.io",
	"k8s.gcr.io":      "https://k8s.gcr.io",
}

// hasNonRootPath returns true if the mirror URL contains a non-root path component,
// which indicates that override_path = true is required.
func hasNonRootPath(mirrorURL string) bool {
	u, err := url.Parse(mirrorURL)
	if err != nil {
		return false
	}
	return strings.Trim(u.Path, "/") != ""
}

// generateHostsTOML builds the content of a hosts.toml file for the given upstream registry
// and mirror URL.
func generateHostsTOML(upstream, mirrorURL string) string {
	var sb strings.Builder

	if upstream != "_default" {
		server, ok := wellKnownUpstreamServers[upstream]
		if !ok {
			server = "https://" + upstream
		}
		sb.WriteString(fmt.Sprintf("server = %q\n", server))
	}

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("[host.%q]\n", mirrorURL))
	sb.WriteString("  capabilities = [\"pull\", \"resolve\"]\n")

	if hasNonRootPath(mirrorURL) {
		sb.WriteString("  override_path = true\n")
	}

	return sb.String()
}

// writeRegistryMirrorFiles writes hosts.toml files for each configured registry mirror
// into the containerd certs.d directory. It is a no-op when mirrors is nil or empty.
// Kubesolo only writes files for registries it is explicitly configured to manage;
// manually created hosts.toml files for other registries are never touched.
func (s *service) writeRegistryMirrorFiles(mirrors map[string]string) error {
	if len(mirrors) == 0 {
		return nil
	}

	if err := filesystem.EnsureDirectoryExists(types.DefaultContainerdCertsDir); err != nil {
		return fmt.Errorf("failed to create certs.d directory: %w", err)
	}

	for upstream, mirrorURL := range mirrors {
		u, err := url.Parse(mirrorURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			log.Warn().Str("component", "containerd").
				Str("upstream", upstream).
				Str("mirror", mirrorURL).
				Msg("skipping registry mirror: invalid URL or unsupported scheme (must be http or https)")
			continue
		}

		dirPath := filepath.Join(types.DefaultContainerdCertsDir, upstream)
		if err := filesystem.EnsureDirectoryExists(dirPath); err != nil {
			return fmt.Errorf("failed to create mirror directory for %s: %w", upstream, err)
		}

		hostsFilePath := filepath.Join(dirPath, "hosts.toml")
		content := generateHostsTOML(upstream, mirrorURL)
		if err := os.WriteFile(hostsFilePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write hosts.toml for %s: %w", upstream, err)
		}

		log.Info().Str("component", "containerd").
			Str("upstream", upstream).
			Str("mirror", mirrorURL).
			Msg("wrote registry mirror hosts.toml")
	}

	return nil
}
