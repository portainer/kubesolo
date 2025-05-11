package containerd

import (
	"context"
	"fmt"
	"slices"

	"github.com/containerd/containerd/v2/client"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
)

// ensureK8sNamespace ensures the k8s.io namespace exists in containerd
// this is a prerequisite for the kubelet to function
func (s *service) ensureK8sNamespace(ctx context.Context, client *client.Client) error {
	namespaces, err := client.NamespaceService().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %v", err)
	}

	exists := slices.Contains(namespaces, types.DefaultK8sNamespace)
	if !exists {
		if err := client.NamespaceService().Create(ctx, types.DefaultK8sNamespace, nil); err != nil {
			return fmt.Errorf("failed to create namespace: %v", err)
		}
		log.Debug().Str("component", "kubelet").Msgf("created %s namespace in containerd", types.DefaultK8sNamespace)
	} else {
		log.Debug().Str("component", "kubelet").Msgf("namespace %s already exists in containerd", types.DefaultK8sNamespace)
	}

	return nil
}
