package coredns

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// coreDNSConfig returns the CoreDNS Corefile for the requested IP-family and
// resolver mode.  In container mode /etc/resolv.conf is empty (kubelet uses
// resolvConf: /dev/null) so hardcoded public resolvers are used instead.
func coreDNSConfig(containerMode bool, disableIPv6 bool) string {
	forward := "forward . /etc/resolv.conf"
	if containerMode {
		forward = "forward . 1.1.1.1 8.8.8.8"
	}

	if disableIPv6 {
		return `.:53 {
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
	` + forward + `
	minimal
	reload
	health :8080
	ready :8181
}`
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

// createConfigMap creates or patches the CoreDNS ConfigMap with the Corefile
// for the selected IP family mode. On update it uses a merge patch so existing
// metadata (labels, annotations) and unrelated data keys are preserved.
func createConfigMap(ctx context.Context, clientset *kubernetes.Clientset, containerMode bool, disableIPv6 bool) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSConfigMapName,
			Namespace: coreDNSNamespace,
		},
		Data: map[string]string{
			"Corefile": coreDNSConfig(containerMode, disableIPv6),
		},
	}

	_, err := clientset.CoreV1().ConfigMaps(coreDNSNamespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsAlreadyExists(err) {
		return err
	}

	patch, err := json.Marshal(map[string]any{
		"data": map[string]string{"Corefile": coreDNSConfig(containerMode, disableIPv6)},
	})
	if err != nil {
		return err
	}
	_, err = clientset.CoreV1().ConfigMaps(coreDNSNamespace).Patch(
		ctx, coreDNSConfigMapName, k8stypes.MergePatchType, patch, metav1.PatchOptions{},
	)
	return err
}
