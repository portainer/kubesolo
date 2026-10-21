package node

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func podWith(name string, owner *metav1.OwnerReference, statuses ...corev1.ContainerStatus) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop", Labels: map[string]string{"pod-template-hash": "7c9d5f"}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: statuses},
	}
	if owner != nil {
		controller := true
		o := *owner
		o.Controller = &controller
		pod.OwnerReferences = []metav1.OwnerReference{o}
	}
	for _, s := range statuses {
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: s.Name, Image: s.Image})
	}
	return pod
}

func waiting(name, reason string) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: name, Image: "registry.example.com/shop/" + name + ":1.4", RestartCount: 7,
		State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: "back-off 5m0s"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}}}
}

func running(name string) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: name, Image: "registry.example.com/shop/" + name + ":1.4",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
}

var (
	replicaSet  = &metav1.OwnerReference{Kind: "ReplicaSet", Name: "api-7c9d5f"}
	statefulSet = &metav1.OwnerReference{Kind: "StatefulSet", Name: "db"}
)

func TestFailingPodsAndTheirWorkloads(t *testing.T) {
	cs := fake.NewSimpleClientset(
		podWith("api-7c9d5f-x1", replicaSet, waiting("api", "CrashLoopBackOff")),
		podWith("db-0", statefulSet, waiting("db", "ImagePullBackOff")),
		podWith("lonely", nil, running("sidecar"), waiting("app", "CreateContainerConfigError")),
		podWith("web-7c9d5f-ok", &metav1.OwnerReference{Kind: "ReplicaSet", Name: "web-7c9d5f"}, running("web")),
		podWith("starting", nil, waiting("app", "ContainerCreating")), // not a failure
	)

	got, err := failingPods(context.Background(), cs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"shop/deployment/api": "CrashLoopBackOff",
		"shop/statefulset/db": "ImagePullBackOff",
		"shop/pod/lonely":     "CreateContainerConfigError",
	}
	if len(got) != len(want) {
		t.Fatalf("failing = %+v", got)
	}
	for _, p := range got {
		if want[p.Workload] != p.Reason {
			t.Errorf("%s: reason %s, want %s", p.Workload, p.Reason, want[p.Workload])
		}
	}
}

func TestDescribeAFailingPod(t *testing.T) {
	pod := podWith("api-7c9d5f-x1", replicaSet, waiting("api", "CrashLoopBackOff"), running("proxy"))
	event := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "e1", Namespace: "shop"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "api-7c9d5f-x1", Namespace: "shop"},
		Type:           corev1.EventTypeWarning, Reason: "BackOff", Message: "Back-off restarting failed container api",
		LastTimestamp: metav1.Now(),
	}
	cs := fake.NewSimpleClientset(pod, event)

	state, events, err := describe(context.Background(), cs, failingPodOf(pod, "CrashLoopBackOff"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Name != "shop/deployment/api" || state.Pod != "api-7c9d5f-x1" || len(state.Containers) != 2 {
		t.Fatalf("state = %+v", state)
	}
	api := state.Containers[0]
	if api.Name != "api" || !strings.Contains(api.State, "CrashLoopBackOff") || api.Restarts != 7 || api.LastTermination != "exit 1: Error" {
		t.Errorf("container = %+v", api)
	}
	// The fake API server answers every log request with "fake logs".
	if len(state.Logs) != 1 || state.Logs[0] != "fake logs" {
		t.Errorf("logs = %q", state.Logs)
	}
	if len(events) != 1 || !strings.Contains(events[0], "BackOff") {
		t.Errorf("events = %q", events)
	}
}

// Between restarts a crash-looping container spends much of its time
// terminated, not waiting in CrashLoopBackOff: counted healthy then, its
// outage would start over at every check and never last long enough to be an
// incident.
func TestACrashLoopIsFailingBetweenRestarts(t *testing.T) {
	terminated := func(reason string, code int32, restarts int32) corev1.ContainerStatus {
		return corev1.ContainerStatus{Name: "api", RestartCount: restarts,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code, Reason: reason}}}
	}
	for _, tc := range []struct {
		name   string
		status corev1.ContainerStatus
		want   string
	}{
		{"crashed, between restarts", terminated("Error", 2, 3), "CrashLoopBackOff"},
		{"killed for memory, between restarts", terminated("OOMKilled", 137, 2), "OOMKilled"},
		{"exited cleanly", terminated("Completed", 0, 3), ""},
		{"crashed once, not restarted yet", terminated("Error", 1, 0), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := failingReason(podWith("api-x1", replicaSet, tc.status)); got != tc.want {
				t.Errorf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}
