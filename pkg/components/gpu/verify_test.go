package gpu

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/portainer/kubesolo/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func gpuNode(gpus string) *corev1.Node {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "edge-01"}}
	if gpus != "" {
		node.Status.Allocatable = corev1.ResourceList{"nvidia.com/gpu": resource.MustParse(gpus)}
	}
	return node
}

// finishPods makes every check pod end in phase as soon as it is created.
func finishPods(cs *fake.Clientset, phase corev1.PodPhase, reason string) {
	cs.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pod := action.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		pod.Status.Phase, pod.Status.Reason = phase, reason
		return false, nil, nil
	})
}

var quick = verifyTiming{advertise: 200 * time.Millisecond, pod: 200 * time.Millisecond, poll: 10 * time.Millisecond}

// The GPU is proven usable, not assumed: advertised on the node, then reached
// from a container, where CDI puts nvidia-smi.
func TestVerifyRunsTheGPUInAPod(t *testing.T) {
	cs := fake.NewSimpleClientset(gpuNode("1"))
	finishPods(cs, corev1.PodSucceeded, "")

	got, err := verify(context.Background(), cs, "edge-01", types.GPUVendorNVIDIA, quick)
	if err != nil {
		t.Fatal(err)
	}
	// The fake API server answers every log request with "fake logs".
	if !strings.Contains(got, "fake logs") {
		t.Errorf("result = %q, want the pod's output", got)
	}
	pods, _ := cs.CoreV1().Pods(Namespace).List(context.Background(), metav1.ListOptions{})
	if len(pods.Items) != 0 {
		t.Errorf("the check pod was left behind: %d pods", len(pods.Items))
	}
}

func TestVerifyFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		node   *corev1.Node
		phase  corev1.PodPhase
		reason string
		want   string
	}{
		{"never advertised", gpuNode(""), corev1.PodSucceeded, "", "not advertised"},
		{"the pod fails", gpuNode("1"), corev1.PodFailed, "", "failed"},
		{"every GPU in use", gpuNode("1"), corev1.PodFailed, "UnexpectedAdmissionError", ErrInUse.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := fake.NewSimpleClientset(tc.node)
			finishPods(cs, tc.phase, tc.reason)
			_, err := verify(context.Background(), cs, "edge-01", types.GPUVendorNVIDIA, quick)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

// AMD's plugin puts no tool in a container, so its GPU is checked as far as
// the node advertising it.
func TestVerifyAMD(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "edge-01"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")}}}
	cs := fake.NewSimpleClientset(node)
	got, err := verify(context.Background(), cs, "edge-01", types.GPUVendorAMD, quick)
	if err != nil || !strings.Contains(got, "amd.com/gpu: 1") {
		t.Errorf("got %q, %v", got, err)
	}
}
