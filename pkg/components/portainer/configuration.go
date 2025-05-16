package portainer

import (
	"context"
	"maps"

	"github.com/portainer/kubesolo/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func createConfigMap(ctx context.Context, clientset *kubernetes.Clientset, config types.EdgeAgentConfig) error {
	data := map[string]string{
		"EDGE_ID":            config.EdgeID,
		"EDGE_INSECURE_POLL": config.EdgeInsecurePoll,
	}

	if config.EdgeSecret != "" {
		data["EDGE_SECRET"] = config.EdgeSecret
	}

	maps.Copy(data, config.EnvVars)

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PortainerEdgeAgentConfigMapName,
			Namespace: PortainerNamespace,
		},
		Data: data,
	}

	_, err := clientset.CoreV1().ConfigMaps(PortainerNamespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.CoreV1().ConfigMaps(PortainerNamespace).Update(ctx, configMap, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

func createSecret(ctx context.Context, clientset *kubernetes.Clientset, edgeKey string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PortainerEdgeAgentSecretName,
			Namespace: PortainerNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"edge.key": edgeKey,
		},
	}

	_, err := clientset.CoreV1().Secrets(PortainerNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.CoreV1().Secrets(PortainerNamespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}
