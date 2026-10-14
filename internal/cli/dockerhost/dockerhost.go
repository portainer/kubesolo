// Package dockerhost resolves which container engine kubesoloctl talks to, the
// way the docker CLI does: DOCKER_HOST, otherwise the active Docker context.
//
// Colima, Rancher Desktop, Podman and Docker Desktop each register a context
// pointing at their own socket, and often do not provide /var/run/docker.sock
// at all; kind follows the context through the docker CLI, and kubesoloctl,
// which talks to the engine directly, has to read it itself.
package dockerhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/docker/docker/client"
)

// Host returns the engine endpoint to use: DOCKER_HOST when it is set, else the
// endpoint of the context named by DOCKER_CONTEXT or by currentContext in the
// docker CLI's config.json. It returns "" when neither names one other than
// "default", which means the client's own default, /var/run/docker.sock.
func Host() (string, error) {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h, nil
	}
	dir := configDir()
	name := os.Getenv("DOCKER_CONTEXT")
	if name == "" {
		data, err := os.ReadFile(filepath.Join(dir, "config.json"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		var cfg struct {
			CurrentContext string `json:"currentContext"`
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return "", fmt.Errorf("cannot read %s: %w", filepath.Join(dir, "config.json"), err)
			}
		}
		name = cfg.CurrentContext
	}
	if name == "" || name == "default" {
		return "", nil
	}

	sum := sha256.Sum256([]byte(name))
	meta := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
	data, err := os.ReadFile(meta)
	if err != nil {
		return "", fmt.Errorf("docker context %q not found (%s): %w", name, meta, err)
	}
	var ctx struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &ctx); err != nil {
		return "", fmt.Errorf("cannot read docker context %q: %w", name, err)
	}
	if ctx.Endpoints.Docker.Host == "" {
		return "", fmt.Errorf("docker context %q has no engine endpoint", name)
	}
	return ctx.Endpoints.Docker.Host, nil
}

// configDir is the docker CLI's configuration directory.
func configDir() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docker")
}

// NewClient returns a client of the engine Host resolves.
func NewClient() (*client.Client, error) {
	host, err := Host()
	if err != nil {
		return nil, err
	}
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}
	return client.NewClientWithOpts(opts...)
}
