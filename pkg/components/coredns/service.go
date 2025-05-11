package coredns

import (
	"context"
	"fmt"
	"reflect"

	"github.com/portainer/kubesolo/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func createService(ctx context.Context, clientset *kubernetes.Clientset) error {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSServiceName,
			Namespace: coreDNSNamespace,
			Labels: map[string]string{
				"k8s-app":            "coredns",
				"kubernetes.io/name": "CoreDNS",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"k8s-app": "coredns",
			},
			ClusterIP: types.DefaultCoreDNSIP,
			Ports: []corev1.ServicePort{
				{
					Name:     "dns",
					Port:     53,
					Protocol: corev1.ProtocolUDP,
				},
				{
					Name:     "dns-tcp",
					Port:     53,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}

	existingService, err := clientset.CoreV1().Services(coreDNSNamespace).Get(ctx, coreDNSServiceName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = clientset.CoreV1().Services(coreDNSNamespace).Create(ctx, service, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create CoreDNS service: %v", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get existing CoreDNS service: %v", err)
	}

	if !reflect.DeepEqual(existingService.Spec, service.Spec) {
		err = clientset.CoreV1().Services(coreDNSNamespace).Delete(ctx, coreDNSServiceName, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete existing CoreDNS service: %v", err)
		}

		_, err = clientset.CoreV1().Services(coreDNSNamespace).Create(ctx, service, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to recreate CoreDNS service with static IP: %v", err)
		}
	}
	return nil
}
