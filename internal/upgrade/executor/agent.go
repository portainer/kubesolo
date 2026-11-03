package executor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/portainer/kubesolo/pkg/components/portainer"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// agentCheck verifies that a restart left the Portainer agent running. It is
// an interface so that tests can stand in for the cluster.
type agentCheck interface {
	// snapshot records the agent as it is before KubeSolo is stopped.
	snapshot(ctx context.Context) (*agentState, error)

	// verify waits until deadline for the agent to run as it did before the
	// restart, or, when KubeSolo changed its pod template on starting, for
	// that change to roll out. A nil before is an agent that could not be
	// read, which only has to be running.
	verify(ctx context.Context, before *agentState, deadline time.Time) error
}

// agentState is the agent Deployment and the pod running it.
type agentState struct {
	// deployment is nil when there is no agent.
	deployment *appsv1.Deployment

	// pod and container identify what runs the agent: a restart that keeps
	// the agent keeps both. Empty when no pod runs it.
	pod       string
	container string
	ready     bool

	// lastExit is when the agent's container last exited, zero if it never
	// has.
	lastExit time.Time
}

// crashLoopWindow is the kubelet's longest back-off between restarts of a
// crashing container. A container that exited within it may be in a crash
// loop, ready only between crashes.
const crashLoopWindow = 5 * time.Minute

// steady reports whether the agent was running and had not exited lately, so
// that a restart replacing its container is what disrupted it.
func (s *agentState) steady(now time.Time) bool {
	return s.ready && now.Sub(s.lastExit) > crashLoopWindow
}

// clusterAgent checks the agent through the Kubernetes API.
type clusterAgent struct {
	client   func() (kubernetes.Interface, error)
	interval time.Duration
}

func newClusterAgent(kubeconfig string) *clusterAgent {
	return &clusterAgent{
		client: func() (kubernetes.Interface, error) {
			c, err := newClient(kubeconfig)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		interval: gateInterval,
	}
}

// AgentProblem says why the Portainer agent is not running: its pod is not
// ready, or does not yet run the Deployment's current template. It is "" when
// the agent is running, or when there is none and none is configured. configured is whether
// KubeSolo's configuration sets up an agent; an agent Deployment without one is
// left from an earlier configuration, which the answer says, with what to do.
func AgentProblem(ctx context.Context, kubeconfig string, configured bool) string {
	return newClusterAgent(kubeconfig).problem(ctx, configured)
}

func (a *clusterAgent) problem(ctx context.Context, configured bool) string {
	st, err := a.snapshot(ctx)
	if err != nil {
		return "the Portainer agent could not be read: " + err.Error()
	}
	if st.deployment == nil {
		if configured {
			return "the Portainer agent is not running: its Deployment does not exist"
		}
		return ""
	}
	reason := rolloutPending(st)
	switch {
	case reason != "":
	case st.pod == "":
		reason = "no agent pod is running"
	case !st.ready:
		reason = "the agent's pod is not ready"
	default:
		return ""
	}
	if !configured {
		reason += "; the agent Deployment is left from an earlier configuration that set portainer.edgeID and portainer.edgeKey: put them back, or delete it with `kubectl -n portainer delete deployment portainer-agent`"
	}
	return "the Portainer agent is not running: " + reason
}

func (a *clusterAgent) snapshot(ctx context.Context) (*agentState, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	d, err := client.AppsV1().Deployments(portainer.PortainerNamespace).Get(ctx, portainer.PortainerEdgeAgentDeploymentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return &agentState{}, nil
	}
	if err != nil {
		return nil, err
	}
	st := &agentState{deployment: d}

	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, err
	}
	pods, err := client.CoreV1().Pods(d.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	// The newest running pod is the one a rollout leaves.
	var newest *corev1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil || p.Status.Phase != corev1.PodRunning || len(p.Status.ContainerStatuses) == 0 {
			continue
		}
		if newest == nil || newest.CreationTimestamp.Before(&p.CreationTimestamp) {
			newest = p
		}
	}
	if newest != nil {
		c := newest.Status.ContainerStatuses[0]
		st.pod, st.container, st.ready = string(newest.UID), c.ContainerID, podReady(*newest)
		if t := c.LastTerminationState.Terminated; t != nil {
			st.lastExit = t.FinishedAt.Time
		}
	}
	return st, nil
}

func (a *clusterAgent) verify(ctx context.Context, before *agentState, deadline time.Time) error {
	var reason string
	for {
		now, err := a.snapshot(ctx)
		switch {
		case err != nil:
			reason = err.Error()
		case before == nil || templateChanged(before, now):
			if reason = rolloutPending(now); reason == "" {
				return nil
			}
		default:
			if err := sameAgent(before, now); err != nil {
				return err
			}
			if now.ready {
				return nil
			}
			reason = "the agent's pod is not ready"
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("the Portainer agent is not running: %s", reason)
		}
		select {
		case <-ctx.Done():
			return errInterrupted
		case <-time.After(a.interval):
		}
	}
}

// templateChanged reports whether KubeSolo changed the agent's pod template
// on starting, which replaces its pod by design.
func templateChanged(before, now *agentState) bool {
	return before.deployment != nil && now.deployment != nil &&
		!equality.Semantic.DeepEqual(before.deployment.Spec.Template, now.deployment.Spec.Template)
}

// sameAgent fails when the agent that was running before the restart is no
// longer the one running: its pod or its container was replaced.
func sameAgent(before, now *agentState) error {
	switch {
	case now.pod == "":
		return errors.New("the Portainer agent is no longer running")
	case now.pod != before.pod:
		return errors.New("the Portainer agent's pod was replaced, so the restart disrupted it")
	case now.container != before.container:
		return errors.New("the Portainer agent's container was restarted, so the restart disrupted it")
	}
	return nil
}

// rolloutPending returns "" once the agent Deployment's pods all run its
// current template and are ready, or why not yet.
func rolloutPending(now *agentState) string {
	d := now.deployment
	if d == nil {
		return ""
	}
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	switch {
	case d.Status.ObservedGeneration < d.Generation:
		return "the agent Deployment's change has not been picked up yet"
	case d.Status.UpdatedReplicas < want:
		return "the agent's new pod has not been created yet"
	// The previous pod stays until the new one is ready.
	case d.Status.Replicas > d.Status.UpdatedReplicas || d.Status.AvailableReplicas < d.Status.UpdatedReplicas:
		return "no ready pod runs " + agentImage(d)
	}
	return ""
}

func agentImage(d *appsv1.Deployment) string {
	if c := d.Spec.Template.Spec.Containers; len(c) > 0 {
		return c[0].Image
	}
	return "the agent"
}

func podReady(p corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
