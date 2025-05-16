package coredns

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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

// createConfigMap creates a configMap with the bare minimum CoreDNS configuration
// it creates a new configmap if it does not exist
// it updates the configmap if it already exists
// it returns an error if it fails
func createConfigMap(ctx context.Context, clientset *kubernetes.Clientset) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSConfigMapName,
			Namespace: coreDNSNamespace,
		},
		Data: map[string]string{
			"Corefile": CoreDNSConfig,
		},
	}

	_, err := clientset.CoreV1().ConfigMaps(coreDNSNamespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.CoreV1().ConfigMaps(coreDNSNamespace).Update(ctx, configMap, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}
