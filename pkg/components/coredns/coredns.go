package coredns

import (
	"context"
	"fmt"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
)

const (
	coreDNSNamespace          = "kube-system"
	coreDNSServiceName        = "kube-dns"
	coreDNSConfigMapName      = "coredns"
	coreDNSDeploymentName     = "coredns"
	coreDNSServiceAccountName = "coredns"
	coreDNSClusterRoleName    = "system:coredns"
)

// Deploy deploys all the necessary Kubernetes resources for CoreDNS
func Deploy(adminKubeconfig string) error {
	time.Sleep(types.DefaultComponentSleep)

	ctx, cancel := context.WithTimeout(context.Background(), types.DefaultContextTimeout)
	defer cancel()

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	if err := createConfigMap(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS ConfigMap: %v", err)
	}

	if err := createServiceAccount(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS ServiceAccount: %v", err)
	}

	if err := createClusterRole(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS ClusterRole: %v", err)
	}

	if err := createClusterRoleBinding(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS ClusterRoleBinding: %v", err)
	}

	if err := createDeployment(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS Deployment: %v", err)
	}

	if err := createService(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS Service: %v", err)
	}
	return nil
}
