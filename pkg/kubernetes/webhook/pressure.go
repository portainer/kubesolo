package webhook

import (
	"context"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	v1qos "k8s.io/kubernetes/pkg/apis/core/v1/helper/qos"
	kubelettypes "k8s.io/kubernetes/pkg/kubelet/types"
)

// pressureConditions are the node conditions under which the kubelet refuses
// new pods.
var pressureConditions = []corev1.NodeConditionType{corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure}

// refusedUnderPressure says why the kubelet would refuse the pod on the node,
// or returns "" if it would admit it. It follows the kubelet's eviction
// manager (pkg/kubelet/eviction, Admit): critical pods are always admitted;
// under memory pressure alone, so is any pod but a best-effort one that does
// not tolerate it; under any other pressure, none is.
//
// The scheduler keeps pods off a node under pressure, by its taints. KubeSolo
// has no scheduler — the webhook binds every pod to the node — so without this
// each refused pod is replaced by its controller, and the replacement refused
// in turn, thousands of times.
func refusedUnderPressure(pod *corev1.Pod, node *corev1.Node) string {
	var pressures []corev1.NodeConditionType
	for _, c := range node.Status.Conditions {
		if c.Status == corev1.ConditionTrue && slices.Contains(pressureConditions, c.Type) {
			pressures = append(pressures, c.Type)
		}
	}
	if len(pressures) == 0 || kubelettypes.IsCriticalPod(pod) {
		return ""
	}
	if len(pressures) == 1 && pressures[0] == corev1.NodeMemoryPressure {
		if v1qos.GetPodQOS(pod) != corev1.PodQOSBestEffort {
			return ""
		}
		taint := &corev1.Taint{Key: corev1.TaintNodeMemoryPressure, Effect: corev1.TaintEffectNoSchedule}
		if corev1helpers.TolerationsTolerateTaint(klog.Background(), pod.Spec.Tolerations, taint, false) {
			return ""
		}
	}
	return fmt.Sprintf("the node is under %v, and the kubelet would refuse this pod; its controller will retry once the pressure clears", pressures)
}

// refusalError turns a pod away while the node is under pressure. It is
// expected, not a failure of the webhook.
type refusalError struct{ reason string }

func (e *refusalError) Error() string { return e.reason }

// nodeTimeout bounds reading the node during an admission request.
const nodeTimeout = 2 * time.Second

// node reads this node, or returns nil if it cannot: the webhook then admits
// the pod, as it did before checking.
func (w *Service) node() *corev1.Node {
	cs := w.getClientset()
	if cs == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), nodeTimeout)
	defer cancel()
	node, err := cs.CoreV1().Nodes().Get(ctx, w.nodeName, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	return node
}
