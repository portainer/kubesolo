package coredns

import (
	"context"
	"fmt"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
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

	if err := createService(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS Service: %v", err)
	}

	if err := createDeployment(ctx, clientset); err != nil {
		return fmt.Errorf("failed to create CoreDNS Deployment: %v", err)
	}

	if err := waitForCoreDNSReadiness(clientset); err != nil {
		return fmt.Errorf("failed to wait for CoreDNS deployment to be ready: %v", err)
	}
	return nil
}

func waitForCoreDNSReadiness(clientset *kubernetes.Clientset) error {
	// Use context.Background() with no timeout and rely on PollUntilContextTimeout's timeout parameter
	ctx, cancel := context.WithTimeout(context.Background(), types.DefaultCoreDNSReadinessTimeout*2)
	defer cancel()

	log.Info().Str("component", "coredns").Msg("waiting for CoreDNS pods to be ready...")
	err := wait.PollUntilContextTimeout(
		ctx,
		types.DefaultCoreDNSReadinessCheckInterval,
		types.DefaultCoreDNSReadinessTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			pods, err := clientset.CoreV1().Pods(coreDNSNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: "k8s-app=coredns",
			})
			if err != nil {
				return false, err
			}

			for _, pod := range pods.Items {
				if pod.Status.Phase == corev1.PodRunning {
					for _, condition := range pod.Status.Conditions {
						if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
							log.Info().Str("component", "coredns").Str("pod", pod.Name).Msg("CoreDNS pod is ready")
							return true, nil
						}
					}
				}
			}
			return false, nil
		},
	)
	if err != nil {
		return fmt.Errorf("failed to wait for CoreDNS pods to be ready: %v", err)
	}
	return nil
}
