package kubeconfig

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

// d2kFile describes a single cert file to copy from the container.
type d2kFile struct {
	containerPath string
	localName     string
	mode          os.FileMode
}

var d2kFiles = []d2kFile{
	{"/var/lib/kubesolo/pki/ca/ca.crt", "ca.pem", 0o644},
	{"/var/lib/kubesolo/pki/d2k/client.crt", "cert.pem", 0o600},
	{"/var/lib/kubesolo/pki/d2k/client.key", "key.pem", 0o600},
}

// FetchD2KFromContainer copies the D2K mTLS credentials out of the named
// container, writes them to ~/.docker/d2k/<name>/, and creates or updates
// a Docker context named <name> pointing to tcp://127.0.0.1:<port>.
//
// port is the host-mapped ephemeral port for the container's 2376/tcp binding,
// obtained via service.GetContainerD2KPort.
func FetchD2KFromContainer(containerName, name, socketPath string, port int) error {
	if name == "" {
		name = "kubesolo"
	}

	cli, err := newDockerClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to Docker: %w", err)
	}
	defer func() { _ = cli.Close() }()

	_, realHome, realUID, realGID := resolveRealUser()
	certDir := filepath.Join(realHome, ".docker", "d2k", name)
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return fmt.Errorf("failed to create cert directory %s: %w", certDir, err)
	}

	for _, f := range d2kFiles {
		log.Info().Msgf("copying %s from container %q...", f.containerPath, containerName)
		rc, _, err := cli.CopyFromContainer(context.Background(), containerName, f.containerPath)
		if err != nil {
			return fmt.Errorf("docker cp %s:%s failed: %w", containerName, f.containerPath, err)
		}
		data, extractErr := extractFirstFile(rc)
		rc.Close()
		if extractErr != nil {
			return fmt.Errorf("failed to extract %s: %w", f.containerPath, extractErr)
		}
		dst := filepath.Join(certDir, f.localName)
		if err := os.WriteFile(dst, data, f.mode); err != nil {
			return fmt.Errorf("failed to write %s: %w", dst, err)
		}
	}

	if realUID > 0 {
		if err := chownRecursive(certDir, realUID, realGID); err != nil {
			log.Debug().Err(err).Msgf("could not fix ownership of %s", certDir)
		}
	}

	endpoint := fmt.Sprintf("host=tcp://127.0.0.1:%d,ca=%s,cert=%s,key=%s",
		port,
		filepath.Join(certDir, "ca.pem"),
		filepath.Join(certDir, "cert.pem"),
		filepath.Join(certDir, "key.pem"),
	)

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		log.Info().Msgf("cert files written to %s", certDir)
		log.Info().Msg("docker command line tool not found — install docker, then run: kubesoloctl d2k fetch")
		return nil
	}

	return createOrUpdateDockerContext(dockerPath, name, endpoint)
}

// createOrUpdateDockerContext creates a Docker context named name with the
// given endpoint string. If the context already exists it is updated instead.
func createOrUpdateDockerContext(dockerPath, name, endpoint string) error {
	descFlag := "KubeSolo cluster (" + name + ")"

	out, err := exec.Command(dockerPath, "context", "create", name,
		"--description", descFlag,
		"--docker", endpoint,
	).CombinedOutput()
	if err == nil {
		log.Info().Msgf("Docker context %q created", name)
		return nil
	}

	// Context already exists — update it.
	if strings.Contains(string(out), "already exists") {
		out2, err2 := exec.Command(dockerPath, "context", "update", name,
			"--description", descFlag,
			"--docker", endpoint,
		).CombinedOutput()
		if err2 != nil {
			log.Debug().Msgf("docker context update output: %s", strings.TrimSpace(string(out2)))
			return fmt.Errorf("failed to update Docker context %q: %w", name, err2)
		}
		log.Info().Msgf("Docker context %q updated", name)
		return nil
	}

	log.Debug().Msgf("docker context create output: %s", strings.TrimSpace(string(out)))
	return fmt.Errorf("failed to create Docker context %q: %w", name, err)
}

// RemoveD2KContext removes the Docker context and credential directory created
// by `kubesoloctl d2k fetch` for the named instance. It is best-effort and a
// no-op when the docker CLI is absent or neither artifact exists. It returns
// true if it removed the context or the credential directory.
func RemoveD2KContext(name string) bool {
	if name == "" {
		name = "kubesolo"
	}
	removed := false

	// Remove the credential directory (~/.docker/d2k/<name>/).
	_, realHome, _, _ := resolveRealUser()
	certDir := filepath.Join(realHome, ".docker", "d2k", name)
	if _, err := os.Stat(certDir); err == nil {
		if err := os.RemoveAll(certDir); err != nil {
			log.Debug().Err(err).Msgf("could not remove d2k cert dir %s", certDir)
		} else {
			log.Info().Msgf("removed d2k cert dir %s", certDir)
			removed = true
		}
	}

	// Remove the Docker context. -f forces removal even if it is the current
	// context (Docker then falls back to the default). "not found" is fine.
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return removed
	}
	out, err := exec.Command(dockerPath, "context", "rm", "-f", name).CombinedOutput()
	if err != nil {
		if !strings.Contains(string(out), "not found") {
			log.Debug().Msgf("docker context rm %q: %s", name, strings.TrimSpace(string(out)))
		}
	} else {
		log.Info().Msgf("removed Docker context %q", name)
		removed = true
	}
	return removed
}
