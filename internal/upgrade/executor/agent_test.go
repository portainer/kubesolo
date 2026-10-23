package executor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/portainer/kubesolo/pkg/components/portainer"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	oldImage = "docker.io/portainer/agent:2.30.0"
	newImage = "docker.io/portainer/agent:2.31.0"
)

// agentCluster is a fake cluster running the agent Deployment and its pod.
type agentCluster struct {
	t      *testing.T
	client *fake.Clientset
	agent  *clusterAgent
}

func newAgentCluster(t *testing.T) *agentCluster {
	t.Helper()
	c := &agentCluster{t: t, client: fake.NewSimpleClientset()}
	c.agent = &clusterAgent{
		client:   func() (kubernetes.Interface, error) { return c.client, nil },
		interval: 10 * time.Millisecond,
	}

	labels := map[string]string{"app": portainer.PortainerEdgeAgentDeploymentName}
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: portainer.PortainerEdgeAgentDeploymentName, Namespace: portainer.PortainerNamespace, Generation: 1,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "portainer-agent", Image: oldImage}}},
			},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	}
	if _, err := c.client.AppsV1().Deployments(d.Namespace).Create(context.Background(), d, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	c.runPod("pod-1", "containerd://c1", time.Now().Add(-time.Hour), true)
	return c
}

func (c *agentCluster) runPod(uid, container string, created time.Time, ready bool) {
	c.t.Helper()
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: uid, Namespace: portainer.PortainerNamespace, UID: k8stypes.UID(uid),
			Labels: map[string]string{"app": portainer.PortainerEdgeAgentDeploymentName}, CreationTimestamp: metav1.Time{Time: created},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "portainer-agent", ContainerID: container,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
	if _, err := c.client.CoreV1().Pods(p.Namespace).Create(context.Background(), p, metav1.CreateOptions{}); err != nil {
		c.t.Fatal(err)
	}
}

func (c *agentCluster) deletePod(uid string) {
	c.t.Helper()
	if err := c.client.CoreV1().Pods(portainer.PortainerNamespace).Delete(context.Background(), uid, metav1.DeleteOptions{}); err != nil {
		c.t.Fatal(err)
	}
}

// update changes the agent Deployment the way KubeSolo and the Deployment
// controller do.
func (c *agentCluster) update(change func(*appsv1.Deployment)) {
	c.t.Helper()
	deployments := c.client.AppsV1().Deployments(portainer.PortainerNamespace)
	d, err := deployments.Get(context.Background(), portainer.PortainerEdgeAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		c.t.Fatal(err)
	}
	change(d)
	if _, err := deployments.Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
		c.t.Fatal(err)
	}
}

// changeImage is KubeSolo moving the agent to image, before the Deployment
// controller has acted on it.
func (c *agentCluster) changeImage(image string) {
	c.update(func(d *appsv1.Deployment) {
		d.Spec.Template.Spec.Containers[0].Image = image
		d.Generation++
	})
}

// rollOut is the Deployment controller's progress: updated pods on the
// current template, of which available are ready.
func (c *agentCluster) rollOut(replicas, updated, available int32) {
	c.update(func(d *appsv1.Deployment) {
		d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation, Replicas: replicas, UpdatedReplicas: updated, AvailableReplicas: available}
	})
}

func (c *agentCluster) verify(before *agentState, within time.Duration) error {
	return c.agent.verify(context.Background(), before, time.Now().Add(within))
}

func (c *agentCluster) snapshot() *agentState {
	c.t.Helper()
	st, err := c.agent.snapshot(context.Background())
	if err != nil {
		c.t.Fatal(err)
	}
	return st
}

func TestAgentSurvivingTheRestartPasses(t *testing.T) {
	c := newAgentCluster(t)
	if err := c.verify(c.snapshot(), time.Second); err != nil {
		t.Errorf("the same pod and container: %v", err)
	}
}

// The KillMode=control-group failure: KubeSolo comes back healthy, but every
// container was restarted, the agent's among them.
func TestAgentContainerRestartedFails(t *testing.T) {
	c := newAgentCluster(t)
	before := c.snapshot()
	c.deletePod("pod-1")
	c.runPod("pod-1", "containerd://c2", time.Now().Add(-time.Hour), true)
	if err := c.verify(before, time.Second); err == nil || !strings.Contains(err.Error(), "container was restarted") {
		t.Errorf("err = %v", err)
	}
}

func TestAgentPodReplacedWithoutReasonFails(t *testing.T) {
	c := newAgentCluster(t)
	before := c.snapshot()
	c.deletePod("pod-1")
	c.runPod("pod-2", "containerd://c2", time.Now(), true)
	if err := c.verify(before, time.Second); err == nil || !strings.Contains(err.Error(), "pod was replaced") {
		t.Errorf("err = %v", err)
	}
}

// When KubeSolo changes the agent's pod template on starting, the pod is
// replaced by design. What counts then is that the new pod becomes ready.
func TestAgentRolledOutToANewTemplatePasses(t *testing.T) {
	c := newAgentCluster(t)
	before := c.snapshot()
	c.changeImage(newImage)

	done := make(chan error, 1)
	go func() { done <- c.verify(before, 5*time.Second) }()
	time.Sleep(50 * time.Millisecond)
	c.rollOut(2, 1, 1)
	c.runPod("pod-2", "containerd://c2", time.Now(), false)
	time.Sleep(50 * time.Millisecond)
	c.deletePod("pod-1")
	c.rollOut(1, 1, 1)

	if err := <-done; err != nil {
		t.Errorf("rolled out to the new template: %v", err)
	}
}

// A new template that never runs, such as one naming an image that does not
// exist, fails the restart once the time runs out.
func TestAgentNewTemplateNeverReadyFails(t *testing.T) {
	c := newAgentCluster(t)
	before := c.snapshot()
	c.changeImage("docker.io/portainer/agent:does-not-exist")
	// The previous pod stays available while the new one cannot start.
	c.rollOut(2, 1, 1)

	err := c.verify(before, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no ready pod runs docker.io/portainer/agent:does-not-exist") {
		t.Errorf("err = %v", err)
	}
}

// An agent that could not be read before the restart only has to be running
// after it.
func TestAgentUnknownBeforeOnlyHasToBeRunning(t *testing.T) {
	c := newAgentCluster(t)
	if err := c.verify(nil, time.Second); err != nil {
		t.Errorf("running agent: %v", err)
	}
	c.rollOut(1, 1, 0)
	if err := c.verify(nil, 100*time.Millisecond); err == nil || !strings.Contains(err.Error(), "no ready pod runs") {
		t.Errorf("agent not ready: %v", err)
	}
}

func TestSnapshotRecordsTheAgentsLastExit(t *testing.T) {
	c := newAgentCluster(t)
	if st := c.snapshot(); !st.lastExit.IsZero() {
		t.Errorf("never exited, lastExit = %v", st.lastExit)
	}
	exited := time.Now().Add(-time.Minute).Truncate(time.Second)
	pods := c.client.CoreV1().Pods(portainer.PortainerNamespace)
	p, err := pods.Get(context.Background(), "pod-1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p.Status.ContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(exited)}
	if _, err := pods.Update(context.Background(), p, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if st := c.snapshot(); !st.lastExit.Equal(exited) || st.steady(time.Now()) {
		t.Errorf("lastExit = %v, want %v; steady = %v", st.lastExit, exited, st.steady(time.Now()))
	}
}

func TestNoAgentDeploymentSnapshotsAsNoAgent(t *testing.T) {
	c := newAgentCluster(t)
	if err := c.client.AppsV1().Deployments(portainer.PortainerNamespace).Delete(context.Background(), portainer.PortainerEdgeAgentDeploymentName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if st := c.snapshot(); st == nil || st.deployment != nil || st.ready {
		t.Errorf("snapshot = %+v, want no agent", st)
	}
}

func TestAgentCheckInterrupted(t *testing.T) {
	c := newAgentCluster(t)
	before := c.snapshot()
	c.changeImage(newImage)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.agent.verify(ctx, before, time.Now().Add(time.Minute)); err != errInterrupted {
		t.Errorf("err = %v, want errInterrupted", err)
	}
}

func TestAgentProblem(t *testing.T) {
	ctx := context.Background()
	c := newAgentCluster(t)
	if p := c.agent.problem(ctx, true); p != "" {
		t.Errorf("a ready agent on its current template: %q", p)
	}

	c.changeImage(newImage)
	c.rollOut(2, 1, 1)
	if p := c.agent.problem(ctx, true); !strings.Contains(p, "the Portainer agent is not running: no ready pod runs "+newImage) || strings.Contains(p, "earlier configuration") {
		t.Errorf("an agent whose new pod is not ready: %q", p)
	}

	// The edge settings were removed by hand: KubeSolo no longer manages the
	// agent it deployed, and says so.
	if p := c.agent.problem(ctx, false); !strings.Contains(p, "left from an earlier configuration") || !strings.Contains(p, "kubectl -n portainer delete deployment portainer-agent") {
		t.Errorf("a broken agent left from an earlier configuration: %q", p)
	}

	if err := c.client.AppsV1().Deployments(portainer.PortainerNamespace).Delete(ctx, portainer.PortainerEdgeAgentDeploymentName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if p := c.agent.problem(ctx, false); p != "" {
		t.Errorf("no agent: %q", p)
	}
	if p := c.agent.problem(ctx, true); !strings.Contains(p, "its Deployment does not exist") {
		t.Errorf("a configured agent with no Deployment: %q", p)
	}
}
