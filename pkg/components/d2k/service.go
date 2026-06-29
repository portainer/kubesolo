package d2k

import (
	"context"

	"github.com/portainer/kubesolo/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// createService reconciles the LoadBalancer Service exposing d2k on port 2376.
// The kubesolo webhook (when --load-balancer is enabled, which is the default)
// observes the Service and patches its Status.LoadBalancer.Ingress with the
// node IP, which the endpoint persister then surfaces to operators.
func createService(ctx context.Context, clientset kubernetes.Interface, namespace string) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServiceName,
			Namespace: namespace,
			Labels:    commonLabels(),
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Selector: map[string]string{
				"app": AppLabel,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "docker-api",
					Protocol:   corev1.ProtocolTCP,
					Port:       types.DefaultD2KPort,
					TargetPort: intstr.FromInt32(types.DefaultD2KPort),
				},
			},
		},
	}

	_, err := clientset.CoreV1().Services(namespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		// Preserve existing ClusterIP so the Update doesn't fail validation.
		existing, getErr := clientset.CoreV1().Services(namespace).Get(ctx, ServiceName, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		svc.Spec.ClusterIP = existing.Spec.ClusterIP
		svc.Spec.ClusterIPs = existing.Spec.ClusterIPs
		svc.ResourceVersion = existing.ResourceVersion
		_, err = clientset.CoreV1().Services(namespace).Update(ctx, svc, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}
