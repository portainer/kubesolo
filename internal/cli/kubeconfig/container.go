package kubeconfig

import (
	"archive/tar"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/docker/docker/client"
	"github.com/portainer/kubesolo/internal/cli/dockerhost"
	"github.com/rs/zerolog/log"
)

const containerKubeconfigPath = "/var/lib/kubesolo/pki/admin/admin.kubeconfig"

// FetchAndMergeFromContainer copies the admin kubeconfig out of the named
// Docker container and merges it into the local user's kubeconfig, under a
// cluster named after the container.
// socketPath overrides the Docker socket; pass "" to use DOCKER_HOST or the
// platform default (/var/run/docker.sock).
func FetchAndMergeFromContainer(containerName, socketPath string) error {
	cli, err := newDockerClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to Docker: %w", err)
	}
	defer func() { _ = cli.Close() }()

	log.Info().Msgf("copying kubeconfig from container %q...", containerName)
	rc, _, err := cli.CopyFromContainer(context.Background(), containerName, containerKubeconfigPath)
	if err != nil {
		return fmt.Errorf("docker cp from %s:%s failed: %w", containerName, containerKubeconfigPath, err)
	}
	defer func() { _ = rc.Close() }()

	// CopyFromContainer returns a tar stream — extract the single file.
	data, err := extractFirstFile(rc)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig from tar stream: %w", err)
	}

	// Point the server at the host-mapped ephemeral port.
	port, err := containerAPIPort(cli, containerName)
	if err != nil {
		return fmt.Errorf("could not discover the API server port of %s: %w", containerName, err)
	}

	_, realHome, realUID, realGID := resolveRealUser()
	return mergeInto(userKubeconfigPath(realHome), data, containerName, fmt.Sprintf("https://127.0.0.1:%d", port), realUID, realGID)
}

// newDockerClient creates a Docker client. If socketPath is non-empty it is
// used as the daemon socket; otherwise DOCKER_HOST or the active Docker
// context applies.
func newDockerClient(socketPath string) (*client.Client, error) {
	if socketPath == "" {
		return dockerhost.NewClient()
	}
	return client.NewClientWithOpts(client.WithAPIVersionNegotiation(), client.WithHost("unix://"+socketPath))
}

// containerAPIPort inspects the container and returns the host port mapped to
// the Kubernetes API server port 6443/tcp.
func containerAPIPort(cli *client.Client, containerName string) (int, error) {
	info, err := cli.ContainerInspect(context.Background(), containerName)
	if err != nil {
		return 0, err
	}
	bindings, ok := info.NetworkSettings.Ports["6443/tcp"]
	if !ok || len(bindings) == 0 {
		return 0, fmt.Errorf("6443/tcp not exposed by container %s", containerName)
	}
	port, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return 0, fmt.Errorf("invalid host port %q: %w", bindings[0].HostPort, err)
	}
	return port, nil
}

// extractFirstFile reads a tar stream and returns the content of the first
// regular file entry. CopyFromContainer always wraps the file in a tar, so
// there is exactly one entry.
func extractFirstFile(r io.Reader) ([]byte, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		return io.ReadAll(tr)
	}
	return nil, fmt.Errorf("no regular file found in tar stream")
}

// GetFromContainer copies the admin kubeconfig out of the named container and
// returns the raw bytes. Unlike WaitForContainerKubeconfig it makes a single
// attempt — use it when the cluster is known to be running.
func GetFromContainer(containerName, socketPath string) ([]byte, error) {
	cli, err := newDockerClient(socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}
	defer func() { _ = cli.Close() }()

	rc, _, err := cli.CopyFromContainer(context.Background(), containerName, containerKubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("docker cp from %s:%s: %w", containerName, containerKubeconfigPath, err)
	}
	defer func() { _ = rc.Close() }()

	return extractFirstFile(rc)
}

// WaitForContainerKubeconfig polls the kubesolo container until the admin
// kubeconfig is generated, then returns the raw bytes. Waits up to 120 seconds,
// as a host install does. Returns an error if the file never appears, the
// container stops or restarts — KubeSolo exited — or Docker is unreachable.
func WaitForContainerKubeconfig(containerName, socketPath string) ([]byte, error) {
	cli, err := newDockerClient(socketPath)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Docker: %w", err)
	}
	defer func() { _ = cli.Close() }()

	log.Info().Msgf("waiting for kubeconfig in container %q (up to 120s)...", containerName)

	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if insp, err := cli.ContainerInspect(context.Background(), containerName); err == nil &&
			(insp.RestartCount > 0 || insp.State == nil || !insp.State.Running) {
			code := 0
			if insp.State != nil {
				code = insp.State.ExitCode
			}
			return nil, fmt.Errorf("KubeSolo exited in container %q (exit code %d)", containerName, code)
		}
		rc, _, err := cli.CopyFromContainer(context.Background(), containerName, containerKubeconfigPath)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		data, err := extractFirstFile(rc)
		_ = rc.Close()
		if err == nil && len(data) > 0 {
			return data, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("KubeSolo did not write its kubeconfig within 120s")
}

// MergeContainerKubeconfig merges a KubeSolo container's admin kubeconfig into
// the real user's kubeconfig under the cluster name cluster — the container's
// name — and points the cluster server at serverURL, the full https://host:port
// value discovered from Docker.
func MergeContainerKubeconfig(data []byte, cluster, serverURL string) error {
	if len(data) == 0 {
		return fmt.Errorf("no kubeconfig to merge")
	}
	_, realHome, realUID, realGID := resolveRealUser()
	return mergeInto(userKubeconfigPath(realHome), data, cluster, serverURL, realUID, realGID)
}

// WaitForAPIServer polls the Kubernetes API server at addr (host:port) until it
// responds to HTTPS requests or timeout expires. Uses InsecureSkipVerify since
// the caller may not yet have the CA cert loaded in the system trust store.
// Any HTTP response (including 401/403) is treated as "server is up".
func WaitForAPIServer(addr string, timeout time.Duration) error {
	url := "https://" + addr + "/livez"
	httpClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}

	log.Info().Msgf("waiting for API server at %s (up to %s)...", addr, timeout.Round(time.Second))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("API server at %s did not become ready within %s", addr, timeout)
}
