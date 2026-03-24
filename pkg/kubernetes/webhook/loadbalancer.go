package webhook

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

// updateLoadBalancerStatus updates the LoadBalancer service status with the node IP
func (s *Service) updateLoadBalancerStatus(namespace, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cs := s.getClientset()
	if cs == nil {
		log.Error().Str("component", "webhook").
			Msg("clientset is nil, cannot update LoadBalancer status")
		return
	}

	err := s.updateLoadBalancerStatusWithRetry(ctx, namespace, name, cs)
	if err != nil {
		log.Error().Str("component", "webhook").
			Str("service", name).
			Str("namespace", namespace).
			Err(err).
			Msg("failed to update LoadBalancer status after retries")
	}
}

func (s *Service) updateLoadBalancerStatusWithRetry(ctx context.Context, namespace, name string, cs kubernetes.Interface) error {
	return wait.ExponentialBackoff(wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   2,
		Steps:    5,
	}, func() (bool, error) {
		svc, err := cs.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			log.Warn().Str("component", "webhook").
				Str("service", name).
				Str("namespace", namespace).
				Err(err).
				Msg("failed to get service, retrying...")
			return false, nil
		}

		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return true, nil
		}

		if len(svc.Status.LoadBalancer.Ingress) > 0 && svc.Status.LoadBalancer.Ingress[0].IP == s.nodeIP {
			return true, nil
		}

		_, err = cs.CoreV1().Services(namespace).Patch(
			ctx,
			name,
			types.MergePatchType,
			s.loadBalancerStatusPatch,
			metav1.PatchOptions{},
			"status",
		)
		if err != nil {
			log.Warn().Str("component", "webhook").
				Str("service", name).
				Str("namespace", namespace).
				Err(err).
				Msg("failed to patch service status, retrying...")
			return false, nil
		}

		log.Info().Str("component", "webhook").
			Str("service", name).
			Str("namespace", namespace).
			Str("ip", s.nodeIP).
			Msg("updated LoadBalancer status")
		return true, nil
	})
}
