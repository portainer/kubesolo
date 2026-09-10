package kubelet

import (
	"fmt"
	"time"

	kubesolokubernetes "github.com/portainer/kubesolo/internal/kubernetes"
	"github.com/portainer/kubesolo/types"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// runExternal attaches to a kubelet the host manages, instead of starting the
// kubelet KubeSolo embeds. It is selected by kubernetes.kubelet.external.
//
// The asymmetry with an external container runtime is worth stating, because it
// decides the shape of this code. KubeSolo dials a CRI runtime, so attaching to
// one means connecting to a socket. Nothing in KubeSolo dials the kubelet — the
// kubelet dials the API server, and the API server learns where to reach it back
// from the Node object. So there is no endpoint to point at here: attaching means
// waiting for the host's kubelet to register NodeName, and that registration is
// both the readiness signal and the proof the handshake worked.
//
// KubeSolo owns no process in this mode, so there is nothing to supervise and
// nothing to terminate.
func (s *service) runExternal(apiServerReady chan struct{}) error {
	log.Info().Str("component", "kubelet").Str("node", s.nodeName).Msg("attaching to host-managed kubelet...")

	// The kubeconfig and config file are still written. KubeSolo does not consume
	// them here, but they are exactly the material a host-managed kubelet needs to
	// reach this control plane, and generating them from the same code that serves
	// the embedded kubelet keeps one source of truth for both. A kubelet that
	// enrols by bootstrap token, such as Talos's, ignores them.
	if err := s.generateKubeletKubeconfig(); err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to generate kubelet kubeconfig: %v...", err)
		s.cancel()
		return err
	}

	if err := s.writeKubeletConfigFile(); err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to write kubelet config: %v...", err)
		s.cancel()
		return err
	}

	log.Info().Str("component", "kubelet").
		Str("kubeconfig", s.kubeletKubeConfigFile).
		Str("config", s.kubeletConfigFile).
		Msg("wrote credentials for the host-managed kubelet to use")

	select {
	case <-apiServerReady:
	case <-s.ctx.Done():
		return s.ctx.Err()
	}

	if err := s.applyKubeletRBAC(); err != nil {
		log.Error().Str("component", "kubelet").Msgf("failed to apply RBAC rules: %v...", err)
		s.cancel()
		return err
	}

	node, err := s.waitForNodeRegistration()
	if err != nil {
		log.Error().Str("component", "kubelet").Msgf("host-managed kubelet did not register: %v...", err)
		s.cancel()
		return err
	}

	log.Info().Str("component", "kubelet").
		Str("node", node.Name).
		Str("kubelet-version", node.Status.NodeInfo.KubeletVersion).
		Str("container-runtime", node.Status.NodeInfo.ContainerRuntimeVersion).
		Msg("host-managed kubelet registered and is ready")

	close(s.kubeletReady)

	<-s.ctx.Done()
	log.Debug().Str("component", "kubelet").Msg("context cancelled, detaching from host-managed kubelet")

	return nil
}

// waitForNodeRegistration blocks until the host's kubelet has registered
// s.nodeName and reports Ready. The retry budget is the same startup timeout
// every other component uses.
//
// Waiting for Ready rather than for the Node object alone is what makes this a
// usable gate: kube-proxy and CoreDNS start behind this channel and both need a
// node that can actually run pods.
func (s *service) waitForNodeRegistration() (*corev1.Node, error) {
	clientset, err := kubesolokubernetes.GetKubernetesClient(s.adminKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	// Everything below is judged against this instant, because the Node object
	// outlives the kubelet that created it. After a reboot it is still in the
	// datastore reporting Ready, and stays that way until the node lifecycle
	// controller ages out the lease — so Ready alone would open this gate while
	// the host's kubelet is still starting.
	since := time.Now()

	var lastErr error
	for attempt := range types.DefaultRetryCount {
		node, err := clientset.CoreV1().Nodes().Get(s.ctx, s.nodeName, metav1.GetOptions{})
		switch {
		case err != nil:
			lastErr = err
			// The distinction matters to whoever reads this log: a node that has
			// not appeared is an enrolment problem on the host's side, whereas a
			// node that is present but not Ready is usually CNI.
			log.Warn().Str("component", "kubelet").
				Msgf("waiting for the host's kubelet to register node %q with this control plane (attempt %d/%d)...", s.nodeName, attempt+1, types.DefaultRetryCount)
		case !isNodeReady(node):
			lastErr = fmt.Errorf("node %s is registered but not Ready", s.nodeName)
			log.Warn().Str("component", "kubelet").
				Msgf("node %q has registered but is not Ready yet (attempt %d/%d)...", s.nodeName, attempt+1, types.DefaultRetryCount)
		case !s.heartbeatSince(clientset, since):
			lastErr = fmt.Errorf("node %s reports Ready but has not renewed its lease since this run started", s.nodeName)
			log.Warn().Str("component", "kubelet").
				Msgf("node %q is Ready from a previous run; waiting for the host's kubelet to renew its lease (attempt %d/%d)...", s.nodeName, attempt+1, types.DefaultRetryCount)
		default:
			return node, nil
		}

		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case <-time.After(types.DefaultComponentSleep):
		}
	}

	return nil, fmt.Errorf("node %s did not become ready: %v (check that the host's kubelet is running, that kubernetes.nodeName matches the name it registers under, and that kubernetes.bootstrapToken is set if it enrols by token)", s.nodeName, lastErr)
}

func isNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// heartbeatSince reports whether the host's kubelet has renewed the node's
// lease since the given instant, which is the only evidence that the kubelet
// answering for this Node is the one running now.
//
// The lease is used rather than the Ready condition's timestamps because the
// kubelet renews the lease every few seconds while it updates Node status only
// on change, so a stale Node can carry a Ready condition minutes old.
func (s *service) heartbeatSince(clientset *kubernetes.Clientset, since time.Time) bool {
	lease, err := clientset.CoordinationV1().Leases(corev1.NamespaceNodeLease).Get(s.ctx, s.nodeName, metav1.GetOptions{})
	if err != nil {
		return false
	}

	// A lease with no renew time has been created but never renewed.
	return lease.Spec.RenewTime != nil && lease.Spec.RenewTime.After(since)
}
