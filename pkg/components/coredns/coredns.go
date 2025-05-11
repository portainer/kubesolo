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

// CoreDNSConfig contains minimal CoreDNS Corefile configuration
const CoreDNSConfig = `.:53 {
	errors
	cache 30
	kubernetes cluster.local in-addr.arpa ip6.arpa {
		pods insecure
		endpoint_pod_names
		ttl 30
	}
	forward . 1.1.1.1 8.8.8.8
	health :8080
}`

// Deploy deploys CoreDNS to the cluster
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
