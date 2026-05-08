package coredns

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const coreDNSConfigIPv4Only = `.:53 {
	errors
	loop
	cache 30 {
		disable denial cluster.local
	}
	kubernetes cluster.local in-addr.arpa {
		pods insecure
		fallthrough in-addr.arpa
		ttl 30
	}
	forward . /etc/resolv.conf
	minimal
	reload
	health :8080
	ready :8181
}`

const coreDNSConfigDualStack = `.:53 {
	errors
	loop
	cache 30 {
		disable denial cluster.local
	}
	kubernetes cluster.local in-addr.arpa ip6.arpa {
		pods insecure
		fallthrough in-addr.arpa ip6.arpa
		ttl 30
	}
	forward . /etc/resolv.conf
	minimal
	reload
	health :8080
	ready :8181
}`

func coreDNSConfig(disableIPv6 bool) string {
	if disableIPv6 {
		return coreDNSConfigIPv4Only
	}
	return coreDNSConfigDualStack
}

// createConfigMap creates a configMap with the bare minimum CoreDNS configuration
// it creates a new configmap if it does not exist
// it updates the configmap if it already exists
// it returns an error if it fails
func createConfigMap(ctx context.Context, clientset *kubernetes.Clientset, disableIPv6 bool) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSConfigMapName,
			Namespace: coreDNSNamespace,
		},
		Data: map[string]string{
			"Corefile": coreDNSConfig(disableIPv6),
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
