package portainer

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func createHeadlessService(ctx context.Context, clientset *kubernetes.Clientset) error {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PortainerEdgeAgentServiceName,
			Namespace: PortainerNamespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:                "None",
			PublishNotReadyAddresses: true,
			Selector: map[string]string{
				"app": PortainerEdgeAgentDeploymentName,
			},
			Ports: []corev1.ServicePort{
				{
					Protocol: corev1.ProtocolTCP,
					Port:     9001,
					Name:     "edge",
				},
				{
					Protocol: corev1.ProtocolTCP,
					Port:     80,
					Name:     "http",
				},
			},
		},
	}

	// Create only if absent. On reboot the Service is restored from kine, so we
	// leave it untouched to preserve any Portainer-managed changes.
	_, err := clientset.CoreV1().Services(PortainerNamespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
