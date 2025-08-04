package webhook

import (
	"encoding/json"

	"github.com/rs/zerolog/log"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// createNodeNamePatch creates a patch to set the node name for the pod
func (w *Service) createNodeNamePatch(pod corev1.Pod) []map[string]any {
	log.Info().Str("component", "webhook").
		Str("pod", pod.Name).
		Str("namespace", pod.Namespace).
		Str("node", w.nodeName).
		Msg("setting node name for pod")

	// Use pre-computed patch to reduce CPU usage
	var patch []map[string]any
	json.Unmarshal(w.nodeNamePatch, &patch)
	return patch
}

// createNodeSelectorPatch creates a patch to set the node selector for the job
func (w *Service) createNodeSelectorPatch(job batchv1.Job) []map[string]any {
	log.Info().Str("component", "webhook").
		Str("job", job.Name).
		Str("namespace", job.Namespace).
		Str("node", w.nodeName).
		Msg("setting node selector for job")

	var patch []map[string]any
	json.Unmarshal(w.nodeSelectorPatch, &patch)
	return patch
}
