package portainer

import (
	"context"
	"fmt"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
)

const (
	PortainerNamespace                       = "portainer"
	PortainerEdgeAgentDeploymentName         = "portainer-agent"
	PortainerEdgeAgentServiceName            = "portainer-agent"
	PortainerEdgeAgentConfigMapName          = "portainer-agent-edge"
	PortainerEdgeAgentSecretName             = "portainer-agent-edge-key"
	PortainerEdgeAgentServiceAccountName     = "portainer-sa-clusteradmin"
	ClusterAdminClusterRoleName              = "cluster-admin"
	PortainerEdgeAgentClusterRoleBindingName = "portainer-crb-clusteradmin"
)

// DeployEdgeAgent deploys Portainer Edge Agent to the cluster
func DeployEdgeAgent(adminKubeconfig string, config types.EdgeAgentConfig) error {
	time.Sleep(types.DefaultComponentSleep)

	ctx, cancel := context.WithTimeout(context.Background(), types.DefaultContextTimeout)
	defer cancel()

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	if err := createNamespace(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create Portainer namespace: %v", err)
	}

	if err := createConfigMap(ctx, clientset, config); err != nil {
		return fmt.Errorf("failed to create Portainer ConfigMap: %v", err)
	}

	if err := createSecret(ctx, clientset, config.EdgeKey); err != nil {
		return fmt.Errorf("failed to create Portainer Secret: %v", err)
	}

	if err := createServiceAccount(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create Portainer ServiceAccount: %v", err)
	}

	if err := createClusterRoleBinding(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create Portainer ClusterRoleBinding: %v", err)
	}

	if err := createHeadlessService(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create Portainer Headless Service: %v", err)
	}

	if err := createDeployment(ctx, clientset, config); err != nil {
		return fmt.Errorf("failed to create Portainer Deployment: %v", err)
	}
	return nil
}
