// Package node holds the parts of the assistant that talk to the node's
// containerd and Kubernetes. They are kept out of package assistant so that
// kubesoloctl, which uses its API types, does not link either client.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"unicode/utf8"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/remotes/docker/config"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/portainer/kubesolo/pkg/assistant"
)

// Namespace is the containerd namespace models are fetched into. The kubelet
// only cleans up images in its own namespace, and pods cannot see this one.
const Namespace = "kubesolo-assistant"

// ggufMagic opens every GGUF file.
const ggufMagic = "GGUF"

// ErrNotAModel and ErrNotKnowledge mean the artifact is not a model, or not
// a knowledge file, KubeSolo can use. Fetching it again will not change that.
var (
	ErrNotAModel    = errors.New("not a model artifact")
	ErrNotKnowledge = errors.New("not a knowledge artifact")
)

// FetchModel makes the model artifact ref available in containerd's content
// store and returns the path of its GGUF file there, which llama-server reads
// in place. An artifact fetched before is used without contacting the
// registry, so the model survives a registry that has become unreachable.
//
// registryDir holds the hosts.toml files that configure registries;
// containerdRoot is containerd's root directory.
func FetchModel(ctx context.Context, c *client.Client, ref, registryDir, containerdRoot string) (string, error) {
	return fetch(ctx, c, ref, registryDir, containerdRoot, modelFile)
}

// FetchKnowledge is FetchModel for the knowledge artifact, whose single layer
// is the Markdown file.
func FetchKnowledge(ctx context.Context, c *client.Client, ref, registryDir, containerdRoot string) (string, error) {
	return fetch(ctx, c, ref, registryDir, containerdRoot, knowledgeFile)
}

type layerFileFunc func(ctx context.Context, cs content.Provider, target ocispec.Descriptor, contentDir string) (string, error)

func fetch(ctx context.Context, c *client.Client, ref, registryDir, containerdRoot string, file layerFileFunc) (string, error) {
	ctx = namespaces.WithNamespace(ctx, Namespace)

	img, err := c.ImageService().Get(ctx, ref)
	if errdefs.IsNotFound(err) {
		resolver := docker.NewResolver(docker.ResolverOptions{
			Hosts: config.ConfigureHosts(ctx, config.HostOptions{HostDir: config.HostDirFromRoot(registryDir)}),
		})
		img, err = c.Fetch(ctx, ref, client.WithResolver(resolver))
	}
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", ref, err)
	}
	return file(ctx, c.ContentStore(), img.Target, filepath.Join(containerdRoot, "io.containerd.content.v1.content"))
}

// modelFile checks that target is a model artifact whose single layer is a
// GGUF file, and returns where that layer is stored. A model built into an
// ordinary image sits inside a tar archive, which llama-server cannot open
// without unpacking a second copy, so it is refused.
func modelFile(ctx context.Context, cs content.Provider, target ocispec.Descriptor, contentDir string) (string, error) {
	return layerFile(ctx, cs, target, contentDir, ErrNotAModel, func(ra content.ReaderAt) error {
		magic := make([]byte, len(ggufMagic))
		if _, err := ra.ReadAt(magic, 0); err != nil || string(magic) != ggufMagic {
			return errors.New("its layer is not a GGUF file; push the .gguf itself as the layer, e.g. with oras push")
		}
		return nil
	})
}

// knowledgeFile checks that target is a knowledge artifact whose single layer
// is a text file small enough for the model's prompt, and returns where that
// layer is stored.
func knowledgeFile(ctx context.Context, cs content.Provider, target ocispec.Descriptor, contentDir string) (string, error) {
	return layerFile(ctx, cs, target, contentDir, ErrNotKnowledge, func(ra content.ReaderAt) error {
		if ra.Size() > assistant.MaxKnowledgeBytes {
			return fmt.Errorf("its layer is %d bytes, and can be at most %d", ra.Size(), assistant.MaxKnowledgeBytes)
		}
		b := make([]byte, ra.Size())
		if _, err := ra.ReadAt(b, 0); err != nil {
			return err
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			return errors.New("its layer is not text; push the Markdown file itself as the layer, e.g. with oras push")
		}
		return nil
	})
}

// layerFile checks that target is an artifact with a single layer that passes
// check, and returns where that layer is stored. A failure wraps notUsable.
func layerFile(ctx context.Context, cs content.Provider, target ocispec.Descriptor, contentDir string, notUsable error, check func(content.ReaderAt) error) (string, error) {
	if target.MediaType != ocispec.MediaTypeImageManifest {
		return "", fmt.Errorf("%w: it is a multi-platform image (%s); an artifact has a single manifest", notUsable, target.MediaType)
	}
	raw, err := content.ReadBlob(ctx, cs, target)
	if err != nil {
		return "", err
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("read the manifest: %w", err)
	}
	if len(m.Layers) != 1 {
		return "", fmt.Errorf("%w: it must have exactly one layer, but has %d", notUsable, len(m.Layers))
	}

	layer := m.Layers[0]
	ra, err := cs.ReaderAt(ctx, layer)
	if err != nil {
		return "", err
	}
	defer func() { _ = ra.Close() }()
	if err := check(ra); err != nil {
		return "", fmt.Errorf("%w: %w", notUsable, err)
	}

	return filepath.Join(contentDir, "blobs", layer.Digest.Algorithm().String(), layer.Digest.Encoded()), nil
}
