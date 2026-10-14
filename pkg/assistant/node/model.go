// Package node holds the parts of the assistant that talk to the node's
// containerd and Kubernetes. They are kept out of package assistant so that
// kubesoloctl, which uses its API types, does not link either client.
package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/remotes/docker/config"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Namespace is the containerd namespace models are fetched into. The kubelet
// only cleans up images in its own namespace, and pods cannot see this one.
const Namespace = "kubesolo-assistant"

// ggufMagic opens every GGUF file.
const ggufMagic = "GGUF"

// ErrNotAModel means the artifact is not a model KubeSolo can run. Fetching it
// again will not change that.
var ErrNotAModel = errors.New("not a model artifact")

// FetchModel makes the model artifact ref available in containerd's content
// store and returns the path of its GGUF file there, which llama-server reads
// in place. An artifact fetched before is used without contacting the
// registry, so the model survives a registry that has become unreachable.
//
// registryDir holds the hosts.toml files that configure registries;
// containerdRoot is containerd's root directory.
func FetchModel(ctx context.Context, c *client.Client, ref, registryDir, containerdRoot string) (string, error) {
	ctx = namespaces.WithNamespace(ctx, Namespace)

	img, err := c.ImageService().Get(ctx, ref)
	if errdefs.IsNotFound(err) {
		resolver := docker.NewResolver(docker.ResolverOptions{
			Hosts: config.ConfigureHosts(ctx, config.HostOptions{HostDir: config.HostDirFromRoot(registryDir)}),
		})
		img, err = c.Fetch(ctx, ref, client.WithResolver(resolver))
	}
	if err != nil {
		return "", fmt.Errorf("fetch model %s: %w", ref, err)
	}
	return modelFile(ctx, c.ContentStore(), img.Target, filepath.Join(containerdRoot, "io.containerd.content.v1.content"))
}

// modelFile checks that target is a model artifact whose single layer is a
// GGUF file, and returns where that layer is stored. A model built into an
// ordinary image sits inside a tar archive, which llama-server cannot open
// without unpacking a second copy, so it is refused.
func modelFile(ctx context.Context, cs content.Provider, target ocispec.Descriptor, contentDir string) (string, error) {
	if target.MediaType != ocispec.MediaTypeImageManifest {
		return "", fmt.Errorf("%w: it is a multi-platform image (%s); a model artifact has a single manifest", ErrNotAModel, target.MediaType)
	}
	raw, err := content.ReadBlob(ctx, cs, target)
	if err != nil {
		return "", err
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("read model manifest: %w", err)
	}
	if len(m.Layers) != 1 {
		return "", fmt.Errorf("%w: it must have exactly one layer, the GGUF file, but has %d", ErrNotAModel, len(m.Layers))
	}

	layer := m.Layers[0]
	ra, err := cs.ReaderAt(ctx, layer)
	if err != nil {
		return "", err
	}
	defer func() { _ = ra.Close() }()
	magic := make([]byte, len(ggufMagic))
	if _, err := ra.ReadAt(magic, 0); err != nil || string(magic) != ggufMagic {
		return "", fmt.Errorf("%w: its layer is not a GGUF file; push the .gguf itself as the layer, e.g. with oras push", ErrNotAModel)
	}

	return filepath.Join(contentDir, "blobs", layer.Digest.Algorithm().String(), layer.Digest.Encoded()), nil
}
