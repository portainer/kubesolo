package containerd

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// importImages imports the images into the containerd registry
func (s *service) importImages(ctx context.Context, client *client.Client, isPortainerAgent bool) error {
	context := namespaces.WithNamespace(ctx, types.DefaultK8sNamespace)
	if isPortainerAgent {
		if err := s.importImage(context, client, s.portainerAgentImageFile); err != nil {
			return err
		}
	}

	if err := s.importImage(context, client, s.corednsImageFile); err != nil {
		return err
	}

	return nil
}

// importImage imports an image into the containerd registry
func (s *service) importImage(ctx context.Context, client *client.Client, image string) error {
	log.Debug().Str("component", "containerd").Str("image", image).Msg("importing image")
	if _, err := client.ImageService().Get(ctx, image); err != nil {
		if errors.Is(err, errdefs.ErrNotFound) {
			log.Debug().Str("component", "containerd").Str("image", image).Msg("image not found, importing")
			imageFile, err := os.Open(image)
			if err != nil {
				return fmt.Errorf("failed to open image file: %v", err)
			}
			defer imageFile.Close()

			gzipReader, err := gzip.NewReader(imageFile)
			if err != nil {
				return err
			}
			defer gzipReader.Close()

			if _, err := client.Import(ctx, gzipReader); err != nil {
				return fmt.Errorf("failed to import image: %v", err)
			}
		} else {
			return fmt.Errorf("failed to get image: %v", err)
		}
	}

	return nil
}
