package kubeconfig

import (
	"archive/tar"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/rs/zerolog/log"
)

const containerKubeconfigPath = "/var/lib/kubesolo/pki/admin/admin.kubeconfig"

// FetchAndMergeFromContainer copies the admin kubeconfig out of the named
// Docker container and merges it into the local user's ~/.kube/config.
// socketPath overrides the Docker socket; pass "" to use DOCKER_HOST or the
// platform default (/var/run/docker.sock).
func FetchAndMergeFromContainer(containerName, socketPath string) error {
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl not found — install kubectl, then run: kubesoloctl kubeconfig fetch")
	}

	cli, err := newDockerClient(socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to Docker: %w", err)
	}
	defer cli.Close()

	log.Info().Msgf("copying kubeconfig from container %q...", containerName)
	rc, _, err := cli.CopyFromContainer(context.Background(), containerName, containerKubeconfigPath)
	if err != nil {
		return fmt.Errorf("docker cp from %s:%s failed: %w", containerName, containerKubeconfigPath, err)
	}
	defer rc.Close()

	// CopyFromContainer returns a tar stream — extract the single file.
	data, err := extractFirstFile(rc)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig from tar stream: %w", err)
	}

	// Write to a temp file so mergeIntoUserConfig can reference it by path.
	tmp, err := os.CreateTemp("", "kubesolo-kubeconfig-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write kubeconfig to temp file: %w", err)
	}
	tmp.Close()

	_, realHome, realUID, realGID := resolveRealUser()
	realUser := filepath.Base(realHome)
	log.Info().Msgf("merging kubeconfig into %s/.kube/config...", realHome)

	mergeIntoUserConfig(kubectlPath, realUser, realHome, realUID, realGID, tmpPath)

	// Patch the server URL to the host-mapped ephemeral port.
	clusterName := clusterNameFromContainer(containerName)
	if port, err := containerAPIPort(cli, containerName); err == nil {
		patchContainerServerURL(kubectlPath, realHome, clusterName, fmt.Sprintf("https://127.0.0.1:%d", port))
	} else {
		log.Debug().Err(err).Msg("could not discover container API port; server URL not patched")
	}
	return nil
}

// newDockerClient creates a Docker client. If socketPath is non-empty it is
// used as the daemon socket; otherwise DOCKER_HOST / platform defaults apply.
func newDockerClient(socketPath string) (*client.Client, error) {
	opts := []client.Opt{client.WithAPIVersionNegotiation()}
	if socketPath != "" {
		opts = append(opts, client.WithHost("unix://"+socketPath))
	} else {
		opts = append(opts, client.FromEnv)
	}
	return client.NewClientWithOpts(opts...)
}

// clusterNameFromContainer derives the kubeconfig cluster name from a Docker
// container name. The container name "kubesolo" maps to cluster "kubesolo";
// "kubesolo-<name>" maps to cluster "<name>"; anything else is used as-is.
func clusterNameFromContainer(containerName string) string {
	if containerName == "" || containerName == "kubesolo" {
		return "kubesolo"
	}
	const prefix = "kubesolo-"
	if strings.HasPrefix(containerName, prefix) {
		return strings.TrimPrefix(containerName, prefix)
	}
	return containerName
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
	defer cli.Close()

	rc, _, err := cli.CopyFromContainer(context.Background(), containerName, containerKubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("docker cp from %s:%s: %w", containerName, containerKubeconfigPath, err)
	}
	defer rc.Close()

	return extractFirstFile(rc)
}

// WaitForContainerKubeconfig polls the kubesolo container until the admin
// kubeconfig is generated, then returns the raw bytes. Waits up to 60 seconds.
// Returns an error if the file never appears or Docker is unreachable.
func WaitForContainerKubeconfig(containerName, socketPath string) ([]byte, error) {
	cli, err := newDockerClient(socketPath)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Docker: %w", err)
	}
	defer cli.Close()

	log.Info().Msgf("waiting for kubeconfig in container %q (up to 60s)...", containerName)

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		rc, _, err := cli.CopyFromContainer(context.Background(), containerName, containerKubeconfigPath)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		data, err := extractFirstFile(rc)
		rc.Close()
		if err == nil && len(data) > 0 {
			return data, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("kubeconfig not available after 60s — run: kubesoloctl kubeconfig fetch once KubeSolo is ready")
}

// renameKubeconfigEntries replaces the hardcoded "kubesolo" / "kubesolo-admin"
// identifiers that KubeSolo generates with name / name-admin.
// This allows multiple named clusters to coexist in ~/.kube/config.
func renameKubeconfigEntries(data []byte, name string) []byte {
	if name == "" || name == "kubesolo" {
		return data
	}
	s := strings.ReplaceAll(string(data), "kubesolo-admin", name+"-admin")
	s = strings.ReplaceAll(s, "kubesolo", name)
	return []byte(s)
}

// MergeContainerKubeconfig renames the context/cluster/user entries to name,
// writes the result to a temp file, merges it into the real user's
// ~/.kube/config, and patches the cluster server URL to serverURL.
// serverURL should be the full https://host:port value discovered from Docker.
func MergeContainerKubeconfig(data []byte, name, serverURL string) {
	if len(data) == 0 {
		log.Warn().Msg("no kubeconfig data — skipping merge")
		return
	}
	if name == "" {
		name = "kubesolo"
	}

	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		log.Info().Msg("kubectl not found — install kubectl, then run: kubesoloctl kubeconfig fetch")
		return
	}

	tmp, err := os.CreateTemp("", "kubesolo-kubeconfig-*")
	if err != nil {
		log.Warn().Err(err).Msg("failed to create temp file for kubeconfig")
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(renameKubeconfigEntries(data, name)); err != nil {
		tmp.Close()
		log.Warn().Err(err).Msg("failed to write kubeconfig to temp file")
		return
	}
	tmp.Close()

	_, realHome, realUID, realGID := resolveRealUser()
	realUser := filepath.Base(realHome)
	log.Info().Msgf("merging kubeconfig into %s/.kube/config...", realHome)
	mergeIntoUserConfig(kubectlPath, realUser, realHome, realUID, realGID, tmpPath)
	patchContainerServerURL(kubectlPath, realHome, name, serverURL)
}

// PatchContainerServerURL updates the kubesolo cluster server in the real
// user's ~/.kube/config to https://127.0.0.1:6443. Call this after
// WaitAndFetchFromContainer or FetchAndMergeFromContainer in container mode
// so that kubectl connects via Docker's published port mapping.
func PatchContainerServerURL() {
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return
	}
	_, realHome, _, _ := resolveRealUser()
	patchContainerServerURL(kubectlPath, realHome, "kubesolo", "https://127.0.0.1:6443")
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
			resp.Body.Close()
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("API server at %s did not become ready within %s", addr, timeout)
}

func patchContainerServerURL(kubectlPath, realHome, clusterName, serverURL string) {
	kubeConfig := filepath.Join(realHome, ".kube", "config")
	cmd := exec.Command(kubectlPath, "config", "set-cluster", clusterName,
		"--server="+serverURL,
		"--kubeconfig="+kubeConfig)
	cmd.Env = append(os.Environ(), "HOME="+realHome, "KUBECONFIG="+kubeConfig)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Debug().Msgf("set-cluster server: %v: %s", err, strings.TrimSpace(string(out)))
	} else {
		log.Info().Msgf("kubeconfig server patched to %s", serverURL)
	}
}
