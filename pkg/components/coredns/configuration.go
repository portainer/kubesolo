package coredns

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// coreDNSConfig returns the minimal CoreDNS Corefile configuration.
// In container mode, /etc/resolv.conf is empty (kubelet uses resolvConf: /dev/null)
// so we use hardcoded upstream DNS servers instead.
func coreDNSConfig(containerMode bool) string {
	forward := "forward . /etc/resolv.conf"
	if containerMode {
		forward = "forward . 1.1.1.1 8.8.8.8"
	}

	return `.:53 {
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
	` + forward + `
	minimal
	reload
	health :8080
	ready :8181
}`
}

// createConfigMap creates a configMap with the bare minimum CoreDNS configuration
// it creates a new configmap if it does not exist
// it updates the configmap if it already exists
// it returns an error if it fails
func createConfigMap(ctx context.Context, clientset *kubernetes.Clientset, containerMode bool) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSConfigMapName,
			Namespace: coreDNSNamespace,
		},
		Data: map[string]string{
			"Corefile": coreDNSConfig(containerMode),
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
