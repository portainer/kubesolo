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
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/docker/docker/client"
)

// Host returns the engine endpoint to use: DOCKER_HOST when it is set, else the
// endpoint of the context named by DOCKER_CONTEXT or by currentContext in the
// docker CLI's config.json. It returns "" when neither names one other than
// "default", which means the client's own default, /var/run/docker.sock.
func Host() (string, error) {
	e, err := resolve()
	return e.host, err
}

// endpoint is where the engine is, and, for a context, the TLS settings the
// docker CLI stores with it.
type endpoint struct {
	host string
	// context is the context the endpoint came from, empty for DOCKER_HOST and
	// the default.
	context    string
	tlsDir     string
	skipVerify bool
}

func resolve() (endpoint, error) {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return endpoint{host: h}, nil
	}
	dir := configDir()
	name := os.Getenv("DOCKER_CONTEXT")
	if name == "" {
		data, err := os.ReadFile(filepath.Join(dir, "config.json"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return endpoint{}, err
		}
		var cfg struct {
			CurrentContext string `json:"currentContext"`
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return endpoint{}, fmt.Errorf("cannot read %s: %w", filepath.Join(dir, "config.json"), err)
			}
		}
		name = cfg.CurrentContext
	}
	if name == "" || name == "default" {
		return endpoint{}, nil
	}

	sum := sha256.Sum256([]byte(name))
	id := hex.EncodeToString(sum[:])
	meta := filepath.Join(dir, "contexts", "meta", id, "meta.json")
	data, err := os.ReadFile(meta)
	if err != nil {
		return endpoint{}, fmt.Errorf("docker context %q not found (%s): %w", name, meta, err)
	}
	var ctx struct {
		Endpoints struct {
			Docker struct {
				Host          string `json:"Host"`
				SkipTLSVerify bool   `json:"SkipTLSVerify"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &ctx); err != nil {
		return endpoint{}, fmt.Errorf("cannot read docker context %q: %w", name, err)
	}
	if ctx.Endpoints.Docker.Host == "" {
		return endpoint{}, fmt.Errorf("docker context %q has no engine endpoint", name)
	}
	return endpoint{
		host:       ctx.Endpoints.Docker.Host,
		context:    name,
		tlsDir:     filepath.Join(dir, "contexts", "tls", id, "docker"),
		skipVerify: ctx.Endpoints.Docker.SkipTLSVerify,
	}, nil
}

// configDir is the docker CLI's configuration directory.
func configDir() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docker")
}

// NewClient returns a client of the engine Host resolves. For a context, the
// TLS material the docker CLI stores with it is used too, and the DOCKER_*
// environment, which describes DOCKER_HOST, is not.
func NewClient() (*client.Client, error) {
	e, err := resolve()
	if err != nil {
		return nil, err
	}
	if e.context == "" {
		opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
		if e.host != "" {
			opts = append(opts, client.WithHost(e.host))
		}
		return client.NewClientWithOpts(opts...)
	}

	opts := []client.Opt{client.WithAPIVersionNegotiation(), client.WithHost(e.host)}
	tlsConfig, err := e.tlsConfig()
	if err != nil {
		return nil, err
	}
	if tlsConfig != nil {
		opts = append(opts, client.WithHTTPClient(&http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}))
	}
	return client.NewClientWithOpts(opts...)
}

// tlsConfig returns the TLS settings of a context's endpoint, or nil for an
// endpoint without any.
func (e endpoint) tlsConfig() (*tls.Config, error) {
	read := func(file string) ([]byte, error) {
		data, err := os.ReadFile(filepath.Join(e.tlsDir, file))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return data, err
	}
	ca, err := read("ca.pem")
	if err != nil {
		return nil, err
	}
	cert, err := read("cert.pem")
	if err != nil {
		return nil, err
	}
	key, err := read("key.pem")
	if err != nil {
		return nil, err
	}
	if ca == nil && cert == nil && !e.skipVerify {
		return nil, nil
	}

	cfg := &tls.Config{InsecureSkipVerify: e.skipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec // the context asks for it
	if ca != nil {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("docker context %q: invalid CA certificate", e.context)
		}
		cfg.RootCAs = pool
	}
	if cert != nil {
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("docker context %q: invalid client certificate: %w", e.context, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}
