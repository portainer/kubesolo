package localpath

import (
	"context"
	"fmt"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
)

// Deploy creates all the necessary components for local-path-provisioner
func Deploy(adminKubeconfig string) error {
	time.Sleep(types.DefaultComponentSleep)

	ctx, cancel := context.WithTimeout(context.Background(), types.DefaultContextTimeout)
	defer cancel()

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	if err := createNamespace(ctx, clientset); err != nil {
		return err
	}

	if err := createServiceAccount(ctx, clientset); err != nil {
		return err
	}

	if err := createRole(ctx, clientset); err != nil {
		return err
	}

	if err := createClusterRole(ctx, clientset); err != nil {
		return err
	}

	if err := createRoleBinding(ctx, clientset); err != nil {
		return err
	}

	if err := createClusterRoleBinding(ctx, clientset); err != nil {
		return err
	}

	if err := createConfigMap(ctx, clientset); err != nil {
		return err
	}

	if err := createDeployment(ctx, clientset); err != nil {
		return err
	}

	if err := createStorageClass(ctx, clientset); err != nil {
		return err
	}

	return nil
}
