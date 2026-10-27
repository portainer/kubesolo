package node

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NodeReady returns the node's Ready condition as the API server last heard
// it from the kubelet: "Ready", or "NotReady" or "Unknown" with its reason.
func NodeReady(cs kubernetes.Interface) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		list, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", err
		}
		if len(list.Items) == 0 {
			return "", errors.New("no node is registered")
		}
		for _, c := range list.Items[0].Status.Conditions {
			if c.Type != corev1.NodeReady {
				continue
			}
			switch c.Status {
			case corev1.ConditionTrue:
				return "Ready", nil
			case corev1.ConditionFalse:
				return fmt.Sprintf("NotReady: %s: %s", c.Reason, c.Message), nil
			}
			return fmt.Sprintf("Unknown: %s: %s", c.Reason, c.Message), nil
		}
		return "", errors.New("the node has no Ready condition")
	}
}
