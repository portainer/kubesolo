package containerd

import (
	"compress/gzip"
	"context"
	"fmt"
	"os"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// importImages imports the images into the containerd registry
func (s *service) importImages(ctx context.Context, c *client.Client, isPortainerEdge, isD2K bool) error {
	nsCtx := namespaces.WithNamespace(ctx, types.DefaultK8sNamespace)
	if err := s.importImage(nsCtx, c, s.corednsImageFile, types.DefaultCoreDNSImage); err != nil {
		return err
	}

	if err := s.importImage(nsCtx, c, s.sandboxImageFile, types.DefaultSandboxImage); err != nil {
		return err
	}

	if err := s.importImage(nsCtx, c, s.localPathProvisionerImageFile, types.DefaultLocalPathProvisionerImage); err != nil {
		return err
	}

	if isPortainerEdge {
		edgeImage := s.portainerEdgeImage
		if edgeImage == "" {
			edgeImage = types.DefaultPortainerEdgeImage
		}

		if edgeImage == types.DefaultPortainerEdgeImage {
			if err := s.importImage(nsCtx, c, s.portainerEdgeImageFile, edgeImage); err != nil {
				return err
			}
		} else {
			// A custom edge image is never in the embedded tarball, so it has to come
			// from a registry: an empty file path forces the pull path. Pre-pulling it
			// is only a warm cache — the kubelet pulls it again when the pod starts —
			// so a bad reference or an unreachable registry must not stop the node
			// coming up.
			if err := s.importImage(nsCtx, c, "", edgeImage); err != nil {
				log.Warn().Str("component", "containerd").Str("image", edgeImage).Msgf("failed to pull custom portainer edge agent image, the kubelet will retry: %v", err)
			}
		}
	}

	if isD2K {
		if err := s.importImage(nsCtx, c, s.d2kImageFile, types.DefaultD2KImage); err != nil {
			return err
		}
	}

	return nil
}

// importImage imports an image into the containerd registry from a local file.
// If the local file is not found (not embedded), it falls back to pulling from the registry.
func (s *service) importImage(ctx context.Context, c *client.Client, imageFile string, imageRef string) error {
	log.Debug().Str("component", "containerd").Str("image", imageRef).Msg("loading image")

	info, err := os.Stat(imageFile)
	if err != nil || info.Size() == 0 {
		log.Info().Str("component", "containerd").Str("image", imageRef).Msg("embedded image not available, pulling from registry")
		if _, pullErr := c.Pull(ctx, imageRef, client.WithPullUnpack); pullErr != nil {
			return fmt.Errorf("failed to pull image %s: %v", imageRef, pullErr)
		}
		return nil
	}

	log.Debug().Str("component", "containerd").Str("image", imageRef).Msg("importing embedded image")
	f, err := os.Open(imageFile)
	if err != nil {
		return fmt.Errorf("failed to open image file: %v", err)
	}
	defer func() { _ = f.Close() }()

	gzipReader, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gzipReader.Close() }()

	if _, err := c.Import(ctx, gzipReader); err != nil {
		return fmt.Errorf("failed to import image %s: %v", imageRef, err)
	}

	return nil
}
