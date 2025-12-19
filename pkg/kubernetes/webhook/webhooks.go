package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rs/zerolog/log"
	admissionv1 "k8s.io/api/admission/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// serveMutate handles mutation requests
func (w *Service) serveMutate(resp http.ResponseWriter, req *http.Request) {
	if !w.validateRequest(resp, req) {
		return
	}

	admissionReview, err := w.decodeAdmissionReview(req)
	if err != nil {
		http.Error(resp, err.Error(), http.StatusBadRequest)
		return
	}

	// Process resource directly without batching
	patches, err := w.processResource(admissionReview)
	if err != nil {
		log.Error().Str("component", "webhook").
			Str("resourceKind", admissionReview.Request.Kind.Kind).
			Str("resourceName", admissionReview.Request.Name).
			Err(err).
			Msg("failed to process resource")

		// Send error response
		admissionResponse := &admissionv1.AdmissionResponse{
			UID:     admissionReview.Request.UID,
			Allowed: false,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
		admissionReview.Response = admissionResponse
		w.sendResponse(resp, admissionReview, nil)
		return
	}

	w.sendResponse(resp, admissionReview, patches)
}

// validateRequest validates the request where the method is POST
func (w *Service) validateRequest(resp http.ResponseWriter, req *http.Request) bool {
	if req.Method != http.MethodPost {
		http.Error(resp, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// decodeAdmissionReview decodes the admission review request
func (w *Service) decodeAdmissionReview(req *http.Request) (*admissionv1.AdmissionReview, error) {
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

// processResource processes a single resource
func (w *Service) processResource(admissionReview *admissionv1.AdmissionReview) ([]map[string]any, error) {
	var patches []map[string]any

	switch admissionReview.Request.Kind.Kind {
	case "Pod":
		patches = w.processPodMutation(admissionReview)
	case "PersistentVolumeClaim":
		patches = w.processPVCMutation(admissionReview)
	case "Job":
		patches = w.processJobMutation(admissionReview)
	case "Service":
		if w.loadBalancer {
			patches = w.processServiceMutation(admissionReview)
		}
	default:
		// For other resource types, just return empty patches
		// This allows processing of any resource type while preserving node mutations
		log.Debug().Str("component", "webhook").
			Str("resourceKind", admissionReview.Request.Kind.Kind).
			Str("resourceName", admissionReview.Request.Name).
			Msg("no mutations needed for resource type")
	}

	return patches, nil
}

// processPodMutation processes the pod mutation
func (w *Service) processPodMutation(admissionReview *admissionv1.AdmissionReview) []map[string]any {
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

// processPVCMutation processes the PVC mutation
func (w *Service) processPVCMutation(admissionReview *admissionv1.AdmissionReview) []map[string]any {
	var pvc corev1.PersistentVolumeClaim
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, &pvc); err != nil {
		log.Error().Str("component", "webhook").Err(err).Msg("failed to unmarshal PVC")
		return nil
	}

	log.Debug().Str("component", "webhook").
		Str("pvc", pvc.Name).
		Str("namespace", pvc.Namespace).
		Msg("processing PVC")

	if _, exists := pvc.Annotations["volume.kubernetes.io/selected-node"]; exists {
		log.Debug().Str("component", "webhook").
			Str("pvc", pvc.Name).
			Str("namespace", pvc.Namespace).
			Msg("PVC already has node annotation")
		return nil
	}

	log.Info().Str("component", "webhook").
		Str("pvc", pvc.Name).
		Str("namespace", pvc.Namespace).
		Str("node", w.nodeName).
		Msg("setting node annotation for PVC")

	return w.pvcAnnotationPatch
}

// processJobMutation processes the job mutation
func (w *Service) processJobMutation(admissionReview *admissionv1.AdmissionReview) []map[string]any {
	var job batchv1.Job
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, &job); err != nil {
		log.Error().Str("component", "webhook").Err(err).Msg("failed to unmarshal job")
		return nil
	}

	log.Debug().Str("component", "webhook").
		Str("job", job.Name).
		Str("namespace", job.Namespace).
		Msg("processing job")

	log.Info().Str("component", "webhook").
		Str("job", job.Name).
		Str("namespace", job.Namespace).
		Str("node", w.nodeName).
		Msg("setting node selector for job")

	return w.createNodeSelectorPatch(job)
}

// processServiceMutation processes the service mutation for LoadBalancer allocation
func (w *Service) processServiceMutation(admissionReview *admissionv1.AdmissionReview) []map[string]any {
	if w.nodeIP == "" {
		log.Warn().Str("component", "webhook").
			Msg("skipping LoadBalancer service mutation: nodeIP is not configured")
		return nil
	}

	var svc corev1.Service
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, &svc); err != nil {
		log.Error().Str("component", "webhook").Err(err).Msg("failed to unmarshal service")
		return nil
	}

	if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
		log.Info().Str("component", "webhook").
			Str("service", svc.Name).
			Str("namespace", svc.Namespace).
			Str("ip", w.nodeIP).
			Msg("setting external IP for LoadBalancer service")

		go w.updateLoadBalancerStatus(svc.Namespace, svc.Name)
	}

	return nil
}

// sendResponse sends the response to the admission review
func (w *Service) sendResponse(resp http.ResponseWriter, admissionReview *admissionv1.AdmissionReview, patches []map[string]any) {
	admissionResponse := w.createAdmissionResponse(admissionReview, patches)
	admissionReview.Response = admissionResponse

	resp.Header().Set("Content-Type", "application/json")

	data, err := json.Marshal(admissionReview)
	if err != nil {
		http.Error(resp, fmt.Sprintf("failed to marshal response: %v", err), http.StatusInternalServerError)
		return
	}
	resp.Write(data)
	log.Debug().Str("component", "webhook").Msg("webhook response sent")
}

// createAdmissionResponse creates the admission response
func (w *Service) createAdmissionResponse(admissionReview *admissionv1.AdmissionReview, patches []map[string]any) *admissionv1.AdmissionResponse {
	response := &admissionv1.AdmissionResponse{
		UID:     admissionReview.Request.UID,
		Allowed: true,
	}

	if len(patches) > 0 {
		if len(patches) == 1 {
			switch patches[0]["path"] {
			case "/spec/nodeName":
				response.Patch = w.nodeNamePatch
			case "/spec/template/spec/nodeSelector":
				response.Patch = w.nodeSelectorPatch
			default:
				patchBytes, err := json.Marshal(patches)
				if err != nil {
					log.Error().Str("component", "webhook").Err(err).Msg("failed to marshal patch")
					return response
				}
				response.Patch = patchBytes
			}
		} else {
			patchBytes, err := json.Marshal(patches)
			if err != nil {
				log.Error().Str("component", "webhook").Err(err).Msg("failed to marshal patch")
				return response
			}
			response.Patch = patchBytes
		}

		patchType := admissionv1.PatchTypeJSONPatch
		response.PatchType = &patchType
	}

	return response
}
