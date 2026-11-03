package webhook

import (
	"context"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func pendingPod(name string, mutate ...func(*corev1.Pod)) *corev1.Pod {
	pod := burstablePod()
	pod.Name, pod.Namespace = name, "default"
	pod.Status.Phase = corev1.PodPending
	for _, m := range mutate {
		m(pod)
	}
	return pod
}

// bindsMade runs bindStranded against these pods and returns the pods it
// bound.
func bindsMade(t *testing.T, node *corev1.Node, pods ...*corev1.Pod) []string {
	t.Helper()
	var objs []runtime.Object
	for _, p := range pods {
		objs = append(objs, p)
	}
	cs := fake.NewSimpleClientset(objs...)
	var bound []string
	cs.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "binding" {
			return false, nil, nil
		}
		b := action.(k8stesting.CreateAction).GetObject().(*corev1.Binding)
		if b.Target.Kind != "Node" || b.Target.Name != node.Name {
			t.Errorf("bound %s to %+v", b.Name, b.Target)
		}
		bound = append(bound, b.Name)
		return true, nil, nil
	})
	bindStranded(context.Background(), cs, node)
	slices.Sort(bound)
	return bound
}

// A pod created while the webhook was not answering — KubeSolo starting, or a
// request that timed out — is admitted without a node, and with no scheduler
// nothing else would ever bind it.
func TestStrandedPodsAreBound(t *testing.T) {
	node := nodeWith()
	node.Name = "edge-01"

	got := bindsMade(t, node,
		pendingPod("stranded"),
		pendingPod("bound", func(p *corev1.Pod) { p.Spec.NodeName = "edge-01" }),
		pendingPod("finished", func(p *corev1.Pod) { p.Status.Phase = corev1.PodFailed }),
		pendingPod("gated", func(p *corev1.Pod) { p.Spec.SchedulingGates = []corev1.PodSchedulingGate{{Name: "wait"}} }),
		pendingPod("deleting", func(p *corev1.Pod) { now := metav1.Now(); p.DeletionTimestamp = &now }),
	)
	if !slices.Equal(got, []string{"stranded"}) {
		t.Errorf("bound %v, want only the stranded pod", got)
	}
}

// Under pressure, a stranded pod the kubelet would refuse waits, Pending,
// until the pressure clears, rather than being bound only to be refused.
func TestStrandedPodsWaitOutPressure(t *testing.T) {
	node := nodeWith(corev1.NodeDiskPressure)
	node.Name = "edge-01"

	critical := criticalPod()
	critical.Name, critical.Namespace, critical.Status.Phase = "critical", "kube-system", corev1.PodPending

	if got := bindsMade(t, node, pendingPod("web"), critical); !slices.Equal(got, []string{"critical"}) {
		t.Errorf("bound %v, want only the critical pod", got)
	}
}
