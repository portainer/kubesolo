package webhook

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// bindInterval is how often pods the webhook did not bind are looked for.
const bindInterval = 30 * time.Second

// bindStrandedPods binds, every bindInterval until ctx is done, the pods
// that were admitted without a node. The webhook binds each pod as it is
// created, but its failure policy is Ignore: a pod created while it does not
// answer — KubeSolo starting before the webhook is up, or a request timing
// out — is admitted unbound, and with no scheduler nothing else would bind it.
func (w *Service) bindStrandedPods(ctx context.Context) {
	ticker := time.NewTicker(bindInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		cs := w.getClientset()
		if cs == nil {
			continue // not registered yet
		}
		node, err := cs.CoreV1().Nodes().Get(ctx, w.nodeName, metav1.GetOptions{})
		if err != nil {
			continue // the node registers once the kubelet is up
		}
		bindStranded(ctx, cs, node)
	}
}

// bindStranded binds each Pending pod without a node to the node, as the
// scheduler would, except a pod the kubelet would refuse under pressure: that
// one waits, Pending, until the pressure clears.
func bindStranded(ctx context.Context, cs kubernetes.Interface, node *corev1.Node) {
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=,status.phase=Pending"})
	if err != nil {
		log.Debug().Str("component", "webhook").Err(err).Msg("cannot list unbound pods")
		return
	}
	for _, pod := range pods.Items {
		switch {
		case pod.Spec.NodeName != "", pod.Status.Phase != corev1.PodPending,
			pod.DeletionTimestamp != nil, len(pod.Spec.SchedulingGates) > 0:
			continue
		case refusedUnderPressure(&pod, node) != "":
			continue
		}
		binding := &corev1.Binding{
			ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace, UID: pod.UID},
			Target:     corev1.ObjectReference{Kind: "Node", Name: node.Name},
		}
		if err := cs.CoreV1().Pods(pod.Namespace).Bind(ctx, binding, metav1.CreateOptions{}); err != nil {
			log.Warn().Str("component", "webhook").Str("pod", pod.Name).Str("namespace", pod.Namespace).Err(err).Msg("cannot bind a pod admitted without a node")
			continue
		}
		log.Info().Str("component", "webhook").Str("pod", pod.Name).Str("namespace", pod.Namespace).Msg("bound a pod admitted without a node")
	}
}
