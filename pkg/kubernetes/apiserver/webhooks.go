package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	admissionv1 "k8s.io/api/admission/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

// webhoook is a webhook that handles pod mutations for KubeSolo
type webhoook struct {
	server       *http.Server
	nodeName     string
	pkiPath      string
	clientset    *kubernetes.Clientset
	hostsEntries map[string]string
}

// newWebhook creates a new webhook server
func newWebhook(nodeName, pkiPath string) *webhoook {
	return &webhoook{
		nodeName:     nodeName,
		pkiPath:      pkiPath,
		hostsEntries: make(map[string]string),
	}
}

// start starts the webhook server
func (w *webhoook) start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", w.serveMutate)

	w.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", types.DefaultWebhookPort),
		Handler: mux,
	}

	certPath := filepath.Join(w.pkiPath, "webhook", "webhook.crt")
	keyPath := filepath.Join(w.pkiPath, "webhook", "webhook.key")

	log.Info().Str("component", "webhook").Msgf("starting webhook server on :%d", types.DefaultWebhookPort)

	w.startServer(certPath, keyPath)
	w.handleShutdown(ctx)

	return nil
}

func (w *webhoook) startServer(certPath, keyPath string) {
	go func() {
		if err := w.server.ListenAndServeTLS(certPath, keyPath); err != nil && err != http.ErrServerClosed {
			log.Error().Str("component", "webhook").Err(err).Msg("webhook server failed")
		}
	}()
}

func (w *webhoook) handleShutdown(ctx context.Context) {
	go func() {
		<-ctx.Done()
		if err := w.server.Shutdown(context.Background()); err != nil {
			log.Error().Str("component", "webhook").Err(err).Msg("error shutting down webhook server")
		}
	}()
}

// serveMutate handles mutation requests
func (w *webhoook) serveMutate(resp http.ResponseWriter, req *http.Request) {
	if !w.validateRequest(resp, req) {
		return
	}

	admissionReview, err := w.decodeAdmissionReview(req)
	if err != nil {
		http.Error(resp, err.Error(), http.StatusBadRequest)
		return
	}

	patches := w.processPodMutation(admissionReview)
	w.sendResponse(resp, admissionReview, patches)
}

func (w *webhoook) validateRequest(resp http.ResponseWriter, req *http.Request) bool {
	if req.Method != http.MethodPost {
		http.Error(resp, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func (w *webhoook) decodeAdmissionReview(req *http.Request) (*admissionv1.AdmissionReview, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading request body: %v", err)
	}

	var admissionReview admissionv1.AdmissionReview
	if _, _, err := deserializer.Decode(body, nil, &admissionReview); err != nil {
		return nil, fmt.Errorf("error decoding admission review: %v", err)
	}

	if admissionReview.Request == nil {
		return nil, fmt.Errorf("admission review with no request")
	}

	log.Debug().Str("component", "webhook").
		Str("uid", string(admissionReview.Request.UID)).
		Str("kind", admissionReview.Request.Kind.Kind).
		Str("operation", string(admissionReview.Request.Operation)).
		Msg("processing admission review request")

	return &admissionReview, nil
}

func (w *webhoook) processPodMutation(admissionReview *admissionv1.AdmissionReview) []map[string]interface{} {
	if admissionReview.Request.Kind.Kind != "Pod" {
		return nil
	}

	var pod corev1.Pod
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, &pod); err != nil {
		log.Error().Str("component", "webhook").Err(err).Msg("failed to unmarshal pod")
		return nil
	}

	log.Debug().Str("component", "webhook").
		Str("pod", pod.Name).
		Str("namespace", pod.Namespace).
		Str("currentNode", pod.Spec.NodeName).
		Msg("processing pod")

	if pod.Spec.NodeName == "" {
		return w.createNodeNamePatch(pod)
	}

	log.Debug().Str("component", "webhook").
		Str("pod", pod.Name).
		Str("namespace", pod.Namespace).
		Str("node", pod.Spec.NodeName).
		Msg("pod already has node name assigned")
	return nil
}

func (w *webhoook) createNodeNamePatch(pod corev1.Pod) []map[string]interface{} {
	patch := map[string]interface{}{
		"op":    "add",
		"path":  "/spec/nodeName",
		"value": w.nodeName,
	}

	log.Info().Str("component", "webhook").
		Str("pod", pod.Name).
		Str("namespace", pod.Namespace).
		Str("node", w.nodeName).
		Msg("setting node name for pod")

	return []map[string]interface{}{patch}
}

func (w *webhoook) sendResponse(resp http.ResponseWriter, admissionReview *admissionv1.AdmissionReview, patches []map[string]interface{}) {
	admissionResponse := w.createAdmissionResponse(admissionReview, patches)
	admissionReview.Response = admissionResponse

	resp.Header().Set("Content-Type", "application/json")
	json.NewEncoder(resp).Encode(admissionReview)
	log.Debug().Str("component", "webhook").Msg("webhook response sent")
}

func (w *webhoook) createAdmissionResponse(admissionReview *admissionv1.AdmissionReview, patches []map[string]interface{}) *admissionv1.AdmissionResponse {
	response := &admissionv1.AdmissionResponse{
		UID:     admissionReview.Request.UID,
		Allowed: true,
	}

	if len(patches) > 0 {
		patchBytes, err := json.Marshal(patches)
		if err != nil {
			log.Error().Str("component", "webhook").Err(err).Msg("failed to marshal patch")
			return response
		}

		patchType := admissionv1.PatchTypeJSONPatch
		response.PatchType = &patchType
		response.Patch = patchBytes
	}

	return response
}

// RegisterWebhook registers the webhook with the Kubernetes API server
func (w *webhoook) RegisterWebhook(kubeconfig string) error {
	clientset, err := kubesolokubernetes.GetKubernetesClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %v", err)
	}
	w.clientset = clientset

	webhookConfig, err := w.createConfiguration()
	if err != nil {
		return err
	}

	return w.createOrUpdateConfig(webhookConfig)
}

func (w *webhoook) createConfiguration() (*admissionregistrationv1.MutatingWebhookConfiguration, error) {
	caCert, err := os.ReadFile(filepath.Join(w.pkiPath, "webhook", "webhook.crt"))
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %v", err)
	}

	failurePolicy := admissionregistrationv1.Ignore
	sideEffects := admissionregistrationv1.SideEffectClassNone
	timeoutSeconds := int32(30)

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
							APIGroups:   []string{"", "apps"},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
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

func (w *webhoook) createOrUpdateConfig(webhookConfig *admissionregistrationv1.MutatingWebhookConfiguration) error {
	_, err := w.clientset.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(
		context.Background(),
		types.DefaultWebhookName,
		metav1.GetOptions{},
	)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			return w.createConfig(webhookConfig)
		} else if k8serrors.IsAlreadyExists(err) {
			return w.updateConfig(webhookConfig)
		}
		return fmt.Errorf("failed to get webhook configuration: %v", err)
	}

	log.Info().Str("component", "webhook").Msgf("webhook %s registered with API server", types.DefaultWebhookName)
	return nil
}

func (w *webhoook) createConfig(webhookConfig *admissionregistrationv1.MutatingWebhookConfiguration) error {
	_, err := w.clientset.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(
		context.Background(),
		webhookConfig,
		metav1.CreateOptions{},
	)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create webhook configuration: %v", err)
	}
	return nil
}

func (w *webhoook) updateConfig(webhookConfig *admissionregistrationv1.MutatingWebhookConfiguration) error {
	_, err := w.clientset.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(
		context.Background(),
		webhookConfig,
		metav1.UpdateOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to update webhook configuration: %v", err)
	}
	return nil
}
