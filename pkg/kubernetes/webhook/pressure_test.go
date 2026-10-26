package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/apis/scheduling"
)

func nodeWith(pressures ...corev1.NodeConditionType) *corev1.Node {
	node := &corev1.Node{}
	node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionTrue})
	for _, p := range pressures {
		node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{Type: p, Status: corev1.ConditionTrue})
	}
	return node
}

func bestEffortPod() *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web"}}}}
}

func burstablePod() *corev1.Pod {
	pod := bestEffortPod()
	pod.Spec.Containers[0].Resources.Requests = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")}
	return pod
}

func criticalPod() *corev1.Pod {
	pod := bestEffortPod()
	priority := int32(scheduling.SystemCriticalPriority)
	pod.Spec.Priority = &priority
	return pod
}

// The webhook binds every pod to the node itself, so it must turn away the
// pods the kubelet would refuse: bound anyway, each is rejected, replaced by
// its controller, and the replacement rejected in turn, thousands of times.
func TestRefusedUnderPressure(t *testing.T) {
	memoryTolerant := bestEffortPod()
	memoryTolerant.Spec.Tolerations = []corev1.Toleration{{Key: corev1.TaintNodeMemoryPressure, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}
	waitingOnPressure := nodeWith(corev1.NodeDiskPressure)
	waitingOnPressure.Status.Conditions[1].Status = corev1.ConditionFalse

	for _, tc := range []struct {
		name    string
		pod     *corev1.Pod
		node    *corev1.Node
		refused string // "" means admitted, otherwise a word the reason carries
	}{
		{"no pressure", bestEffortPod(), nodeWith(), ""},
		{"a pressure condition that is false", bestEffortPod(), waitingOnPressure, ""},
		{"disk pressure", burstablePod(), nodeWith(corev1.NodeDiskPressure), "DiskPressure"},
		{"PID pressure", burstablePod(), nodeWith(corev1.NodePIDPressure), "PIDPressure"},
		{"a critical pod under disk pressure", criticalPod(), nodeWith(corev1.NodeDiskPressure), ""},
		{"memory pressure, burstable", burstablePod(), nodeWith(corev1.NodeMemoryPressure), ""},
		{"memory pressure, best effort", bestEffortPod(), nodeWith(corev1.NodeMemoryPressure), "MemoryPressure"},
		{"memory pressure, tolerated", memoryTolerant, nodeWith(corev1.NodeMemoryPressure), ""},
		{"memory and disk pressure, tolerated", memoryTolerant, nodeWith(corev1.NodeMemoryPressure, corev1.NodeDiskPressure), "DiskPressure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := refusedUnderPressure(tc.pod, tc.node)
			if tc.refused == "" && got != "" {
				t.Errorf("refused: %s", got)
			}
			if tc.refused != "" && !strings.Contains(got, tc.refused) {
				t.Errorf("got %q, want a refusal mentioning %s", got, tc.refused)
			}
		})
	}
}

func podReview(t *testing.T, pod *corev1.Pod) *admissionv1.AdmissionReview {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatal(err)
	}
	return &admissionv1.AdmissionReview{Request: &admissionv1.AdmissionRequest{
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Operation: admissionv1.Create,
		Object:    runtime.RawExtension{Raw: raw},
	}}
}

// A pod is refused, not bound, while the node is under pressure, and bound as
// before when it is not or when the node cannot be read.
func TestPodsAreRefusedUnderPressure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		node    *corev1.Node
		refused bool
	}{
		{"under disk pressure", nodeWith(corev1.NodeDiskPressure), true},
		{"healthy", nodeWith(), false},
		{"node unreadable", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := NewService("edge-01", "", "", "", false)
			w.readNode = func() *corev1.Node { return tc.node }

			patches, err := w.processResource(podReview(t, burstablePod()))
			var refusal *refusalError
			switch {
			case tc.refused && !errors.As(err, &refusal):
				t.Errorf("got %v, want a refusal", err)
			case !tc.refused && (err != nil || len(patches) != 1 || patches[0]["path"] != "/spec/nodeName"):
				t.Errorf("want the pod bound: patches %v, error %v", patches, err)
			}
		})
	}
}

// A refusal must reach the API server as a denial. Answered as an allowed
// request without the node name, the pod would be created unbound, Pending
// for good — and its controller, seeing it exist, would never replace it.
func TestARefusalIsSentAsADenial(t *testing.T) {
	w := NewService("edge-01", "", "", "", false)
	w.readNode = func() *corev1.Node { return nodeWith(corev1.NodeDiskPressure) }
	review := podReview(t, burstablePod())
	review.Request.UID = "uid-1"
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	w.serveMutate(rec, httptest.NewRequest(http.MethodPost, "/mutate", bytes.NewReader(body)))

	var got admissionv1.AdmissionReview
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	r := got.Response
	if r == nil || r.Allowed || r.UID != "uid-1" || len(r.Patch) != 0 || r.Result == nil || !strings.Contains(r.Result.Message, "DiskPressure") {
		t.Errorf("response = %+v", r)
	}
}
