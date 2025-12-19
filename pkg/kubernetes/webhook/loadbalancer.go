package webhook

import (
	"context"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// updateLoadBalancerStatus updates the LoadBalancer service status with the node IP
func (s *Service) updateLoadBalancerStatus(namespace, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if s.clientset == nil {
		var err error
		s.clientset, err = kubesolokubernetes.GetKubernetesClient(s.adminKubeconfig)
		if err != nil {
			log.Error().Str("component", "webhook").
				Err(err).
				Msg("failed to create kubernetes client for LoadBalancer status update")
			return
		}
	}

	// Retry logic for updating the service status
	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   2,
		Steps:    5,
	}, func() (bool, error) {
		svc, err := s.clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			log.Warn().Str("component", "webhook").
				Str("service", name).
				Str("namespace", namespace).
				Err(err).
				Msg("failed to get service, retrying...")
			return false, nil
		}

		// Only process LoadBalancer services
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return true, nil
		}

		// Check if already set
		if len(svc.Status.LoadBalancer.Ingress) > 0 && svc.Status.LoadBalancer.Ingress[0].IP == s.nodeIP {
			return true, nil
		}

		// Update the service status
		svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{
			{
				IP: s.nodeIP,
			},
		}

		_, err = s.clientset.CoreV1().Services(namespace).UpdateStatus(ctx, svc, metav1.UpdateOptions{})
		if err != nil {
			log.Warn().Str("component", "webhook").
				Str("service", name).
				Str("namespace", namespace).
				Err(err).
				Msg("failed to update service status, retrying...")
			return false, nil
		}

		log.Info().Str("component", "webhook").
			Str("service", name).
			Str("namespace", namespace).
			Str("ip", s.nodeIP).
			Msg("updated LoadBalancer status")
		return true, nil
	})

	if err != nil {
		log.Error().Str("component", "webhook").
			Str("service", name).
			Str("namespace", namespace).
			Err(err).
			Msg("failed to update LoadBalancer status after retries")
	}
}
