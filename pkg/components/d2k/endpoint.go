package d2k

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/internal/runtime/filesystem"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// EndpointFiles holds the on-disk filenames written under the connection
// directory. They are stable across runs so operators can document them
// once and forget.
const (
	ConnectionEnvFile = "connection.env"
	ConnectionTxtFile = "connection.txt"
)

// WaitAndPersistEndpoint blocks until the d2k Service has a populated
// LoadBalancer ingress IP, then writes two files under connectionDir:
//
//   - connection.env: shell-sourceable form (DOCKER_HOST=..., DOCKER_TLS_VERIFY=1).
//     Operators run `source <path>` and then `docker ps` directly.
//   - connection.txt: human-readable copy/paste block including the full
//     `docker -H ... ps` command pre-filled.
//
// All on-disk paths in the output are static — the only value that ever
// changes between machines or restarts is the node IP — so operators can
// cat the files long after the startup log has scrolled past.
//
// On timeout the function returns an error but does not fail the parent
// process; the d2k Deployment is still up and reachable, just without a
// surfaced endpoint.
func WaitAndPersistEndpoint(ctx context.Context, adminKubeconfig, namespace string, certs types.D2KCertificatePaths, connectionDir string) error {
	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	ip, err := waitForLoadBalancerIP(ctx, clientset, namespace)
	if err != nil {
		return err
	}

	if err := writeConnectionFiles(connectionDir, ip, certs); err != nil {
		return err
	}

	endpoint := fmt.Sprintf("tcp://%s:%d", ip, types.DefaultD2KPort)
	envFile := filepath.Join(connectionDir, ConnectionEnvFile)
	txtFile := filepath.Join(connectionDir, ConnectionTxtFile)

	log.Info().Str("component", "d2k").
		Str("endpoint", endpoint).
		Str("env_file", envFile).
		Str("txt_file", txtFile).
		Msgf("d2k Docker endpoint ready: %s — connection details written to %s and %s", endpoint, envFile, txtFile)

	return nil
}

// waitForLoadBalancerIP polls the d2k Service until Status.LoadBalancer.Ingress
// has a non-empty IP. Bound by DefaultRetryCount * DefaultComponentSleep.
func waitForLoadBalancerIP(ctx context.Context, clientset *kubernetes.Clientset, namespace string) (string, error) {
	for i := 0; i < types.DefaultRetryCount; i++ {
		pollCtx, cancel := context.WithTimeout(ctx, types.DefaultContextTimeout)
		svc, err := clientset.CoreV1().Services(namespace).Get(pollCtx, ServiceName, metav1.GetOptions{})
		cancel()

		if err != nil && !errors.IsNotFound(err) {
			log.Debug().Str("component", "d2k").Err(err).Msg("failed to get d2k Service, retrying...")
		} else if err == nil {
			if ip := loadBalancerIP(svc); ip != "" {
				return ip, nil
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(types.DefaultComponentSleep):
		}
	}

	return "", fmt.Errorf("d2k Service %s/%s did not receive a LoadBalancer IP within %d attempts", namespace, ServiceName, types.DefaultRetryCount)
}

// loadBalancerIP returns the first populated ingress IP, or empty string
// if none is set yet. Hostnames are not currently used by the kubesolo
// LB webhook but are accepted as a fallback for future compatibility.
func loadBalancerIP(svc *corev1.Service) string {
	for _, ingress := range svc.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			return ingress.IP
		}
		if ingress.Hostname != "" {
			return ingress.Hostname
		}
	}
	return ""
}

// writeConnectionFiles writes connection.env and connection.txt to disk with
// 0644 permissions. The directory itself is created with 0755 if missing.
// Both files are overwritten on every successful run so a fresh node IP
// is reflected immediately after restart.
func writeConnectionFiles(connectionDir, ip string, certs types.D2KCertificatePaths) error {
	if err := filesystem.EnsureDirectoryExists(connectionDir); err != nil {
		return fmt.Errorf("failed to create d2k connection directory: %v", err)
	}

	endpoint := fmt.Sprintf("tcp://%s:%d", ip, types.DefaultD2KPort)
	certPath := filepath.Dir(certs.ClientCert)

	envContents := fmt.Sprintf(`# kubesolo d2k connection — sourced into the shell to drive the docker CLI.
# This file is regenerated on every kubesolo start; only the node IP changes.
export DOCKER_HOST=%s
export DOCKER_TLS_VERIFY=1
export DOCKER_CERT_PATH=%s
# DOCKER_CERT_PATH expects ca.pem/cert.pem/key.pem; symlinks below are created by kubesolo.
export KUBESOLO_D2K_CA_CERT=%s
export KUBESOLO_D2K_CLIENT_CERT=%s
export KUBESOLO_D2K_CLIENT_KEY=%s
`, endpoint, certPath, certs.CACert, certs.ClientCert, certs.ClientKey)

	txtContents := fmt.Sprintf(`kubesolo d2k Docker-compatible endpoint
=======================================

Endpoint    : %s
Namespace   : translated into the namespace passed via --d2k-namespace.

TLS material (paths are static across machines and restarts; only the
node IP in DOCKER_HOST changes):

  CA cert     : %s
  Client cert : %s
  Client key  : %s

Quick start (sources the env file and runs docker against d2k):

  source %s
  docker ps

Explicit form (no env vars):

  docker -H %s \
    --tlsverify \
    --tlscacert %s \
    --tlscert %s \
    --tlskey %s \
    ps
`, endpoint, certs.CACert, certs.ClientCert, certs.ClientKey,
		filepath.Join(connectionDir, ConnectionEnvFile),
		endpoint, certs.CACert, certs.ClientCert, certs.ClientKey)

	if err := os.WriteFile(filepath.Join(connectionDir, ConnectionEnvFile), []byte(envContents), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %v", ConnectionEnvFile, err)
	}

	if err := os.WriteFile(filepath.Join(connectionDir, ConnectionTxtFile), []byte(txtContents), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %v", ConnectionTxtFile, err)
	}

	if err := writeDockerCertPathSymlinks(certPath, certs); err != nil {
		// Symlinks are a convenience for `DOCKER_CERT_PATH`; failure is
		// non-fatal because the explicit form (--tlscacert/--tlscert/--tlskey)
		// still works against the original files.
		log.Warn().Str("component", "d2k").Err(err).Msg("failed to create DOCKER_CERT_PATH symlinks; explicit --tls* flags still work")
	}

	return nil
}

// writeDockerCertPathSymlinks creates the ca.pem/cert.pem/key.pem layout the
// docker CLI looks for when DOCKER_CERT_PATH is set. It is idempotent —
// existing symlinks are removed and recreated so the targets stay in sync
// after a CA rotation or PKI move.
func writeDockerCertPathSymlinks(dir string, certs types.D2KCertificatePaths) error {
	links := []struct {
		name   string
		target string
	}{
		{"ca.pem", certs.CACert},
		{"cert.pem", certs.ClientCert},
		{"key.pem", certs.ClientKey},
	}

	for _, link := range links {
		path := filepath.Join(dir, link.name)
		if info, err := os.Lstat(path); err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("refusing to remove %s: exists but is not a symlink", path)
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("failed to remove existing symlink %s: %v", path, err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat %s: %v", path, err)
		}
		if err := os.Symlink(link.target, path); err != nil {
			return fmt.Errorf("failed to symlink %s -> %s: %v", path, link.target, err)
		}
	}
	return nil
}
