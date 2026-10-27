package node

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNodeReady(t *testing.T) {
	for _, tc := range []struct {
		name string
		cond corev1.NodeCondition
		want string
	}{
		{"ready", corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionTrue, Reason: "KubeletReady"}, "Ready"},
		{"stopped posting", corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionUnknown,
			Reason: "NodeStatusUnknown", Message: "Kubelet stopped posting node status."},
			"Unknown: NodeStatusUnknown: Kubelet stopped posting node status."},
		{"not ready", corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionFalse,
			Reason: "KubeletNotReady", Message: "container runtime is down"},
			"NotReady: KubeletNotReady: container runtime is down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := fake.NewSimpleClientset(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "edge-1"},
				Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse}, tc.cond}},
			})
			got, err := NodeReady(cs)(context.Background())
			if err != nil || got != tc.want {
				t.Errorf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := NodeReady(fake.NewSimpleClientset())(context.Background()); err == nil {
		t.Error("no node registered: want an error")
	}
}
