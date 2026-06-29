package d2k

import (
	"context"
	"fmt"
	"os"

	"github.com/portainer/kubesolo/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// createTLSSecret syncs the on-disk d2k server certificate and key into a
// kubernetes.io/tls Secret named d2k-tls. The Deployment mounts this Secret
// at /etc/d2k/tls; d2k auto-detects the files at /etc/d2k/tls/tls.crt and
// /etc/d2k/tls/tls.key and listens on port 2376 with TLS when present.
//
// The CA certificate is *not* embedded in this Secret because d2k does not
// require it for serving — operators verify the server cert against the
// kubesolo CA on disk via `docker --tlscacert`.
func createTLSSecret(ctx context.Context, clientset kubernetes.Interface, namespace string, certs types.D2KCertificatePaths) error {
	cert, err := os.ReadFile(certs.ServerCert)
	if err != nil {
		return fmt.Errorf("failed to read d2k server certificate %s: %v", certs.ServerCert, err)
	}

	key, err := os.ReadFile(certs.ServerKey)
	if err != nil {
		return fmt.Errorf("failed to read d2k server key %s: %v", certs.ServerKey, err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TLSSecretName,
			Namespace: namespace,
			Labels:    commonLabels(),
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       cert,
			corev1.TLSPrivateKeyKey: key,
		},
	}

	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}
