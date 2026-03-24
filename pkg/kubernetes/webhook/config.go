package webhook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// createConfiguration creates the webhook configuration
func (w *Service) createConfiguration() (*admissionregistrationv1.MutatingWebhookConfiguration, error) {
	caCert, err := os.ReadFile(filepath.Join(w.pkiPath, "webhook", "webhook.crt"))
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %v", err)
	}

	failurePolicy := admissionregistrationv1.Ignore
	sideEffects := admissionregistrationv1.SideEffectClassNone
	timeoutSeconds := int32(30)

	resources := []string{"pods", "persistentvolumeclaims", "jobs"}
	if w.loadBalancer {
		resources = append(resources, "services")
	}

	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: types.DefaultWebhookName,
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: types.DefaultWebhookName,
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					URL:      kubesolokubernetes.StringPtr("https://127.0.0.1:10443/mutate"),
					CABundle: caCert,
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"", "apps", "batch"},
							APIVersions: []string{"v1"},
							Resources:   resources,
						},
					},
				},
				FailurePolicy:           &failurePolicy,
				SideEffects:             &sideEffects,
				TimeoutSeconds:          &timeoutSeconds,
				AdmissionReviewVersions: []string{"v1"},
				NamespaceSelector:       nil,
				ReinvocationPolicy:      (*admissionregistrationv1.ReinvocationPolicyType)(kubesolokubernetes.StringPtr("IfNeeded")),
			},
		},
	}, nil
}

// createOrUpdateConfig creates or updates the webhook configuration
func (w *Service) createOrUpdateConfig(webhookConfig *admissionregistrationv1.MutatingWebhookConfiguration) error {
	cs := w.getClientset()
	existingConfig, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.Background(), types.DefaultWebhookName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return w.createConfig(webhookConfig)
		}
		return fmt.Errorf("failed to get existing webhook configuration: %v", err)
	}

	return w.updateConfig(webhookConfig, existingConfig)
}

// createConfig creates a new webhook configuration
func (w *Service) createConfig(webhookConfig *admissionregistrationv1.MutatingWebhookConfiguration) error {
	cs := w.getClientset()
	_, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(context.Background(), webhookConfig, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create webhook configuration: %v", err)
	}

	log.Info().Str("component", "webhook").Msg("created webhook configuration")
	return nil
}

// updateConfig updates an existing webhook configuration
func (w *Service) updateConfig(webhookConfig *admissionregistrationv1.MutatingWebhookConfiguration, existingConfig *admissionregistrationv1.MutatingWebhookConfiguration) error {
	webhookConfig.ObjectMeta.ResourceVersion = existingConfig.ObjectMeta.ResourceVersion
	cs := w.getClientset()
	_, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(context.Background(), webhookConfig, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update webhook configuration: %v", err)
	}

	log.Info().Str("component", "webhook").Msg("updated webhook configuration")
	return nil
}
