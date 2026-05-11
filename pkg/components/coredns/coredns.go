package coredns

import (
	"context"
	"fmt"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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
func Deploy(adminKubeconfig string, disableIPv6 bool) error {
	time.Sleep(types.DefaultComponentSleep)

	ctx, cancel := context.WithTimeout(context.Background(), types.DefaultContextTimeout)
	defer cancel()

	clientset, err := kubesolokubernetes.GetKubernetesClient(adminKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	if err := createConfigMap(ctx, clientset, disableIPv6); err != nil {
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

	if err := createService(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS Service: %v", err)
	}

	if err := createDeployment(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS Deployment: %v", err)
	}

	if err := waitForCoreDNSReady(clientset); err != nil {
		return fmt.Errorf("CoreDNS did not become ready: %v", err)
	}

	return nil
}

// waitForCoreDNSReady polls the CoreDNS deployment until at least one replica is ready
func waitForCoreDNSReady(clientset *kubernetes.Clientset) error {
	log.Info().Str("component", "coredns").Msg("waiting for CoreDNS to become ready...")

	for i := range types.DefaultRetryCount * 3 {
		ctx, cancel := context.WithTimeout(context.Background(), types.DefaultContextTimeout)
		deployment, err := clientset.AppsV1().Deployments(coreDNSNamespace).Get(ctx, coreDNSDeploymentName, metav1.GetOptions{})
		cancel()

		if err == nil && deployment.Status.ReadyReplicas > 0 {
			log.Info().Str("component", "coredns").Msg("CoreDNS is ready")
			return nil
		}

		log.Debug().Str("component", "coredns").Msgf("CoreDNS not ready yet (attempt %d), waiting...", i+1)
		time.Sleep(types.DefaultComponentSleep)
	}

	return fmt.Errorf("CoreDNS deployment did not reach ready state after %d attempts", types.DefaultRetryCount*3)
}
