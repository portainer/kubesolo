package node

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/portainer/kubesolo/pkg/assistant"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// failingReasons are the container states that mean a workload is failing,
// rather than starting.
var failingReasons = []string{
	"CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "InvalidImageName",
	"CreateContainerConfigError", "CreateContainerError", "RunContainerError",
}

const (
	// logLines is how many of a failing container's last log lines are read,
	// and maxLogBytes bounds them.
	logLines    = 50
	maxLogBytes = 64 << 10
)

// FailingPods returns a WorkloadWatcher.Failing that reads from the API
// server, and Describe its Describe.
func FailingPods(cs kubernetes.Interface) func(context.Context) ([]assistant.FailingPod, error) {
	return func(ctx context.Context) ([]assistant.FailingPod, error) { return failingPods(ctx, cs) }
}

// Describe returns a WorkloadWatcher.Describe that reads from the API server.
func Describe(cs kubernetes.Interface) func(context.Context, assistant.FailingPod) (*assistant.WorkloadState, []string, error) {
	return func(ctx context.Context, p assistant.FailingPod) (*assistant.WorkloadState, []string, error) {
		return describe(ctx, cs, p)
	}
}

func failingPods(ctx context.Context, cs kubernetes.Interface) ([]assistant.FailingPod, error) {
	list, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []assistant.FailingPod
	for i := range list.Items {
		pod := &list.Items[i]
		if reason := failingReason(pod); reason != "" {
			out = append(out, failingPodOf(pod, reason))
		}
	}
	return out, nil
}

func failingPodOf(pod *corev1.Pod, reason string) assistant.FailingPod {
	return assistant.FailingPod{Workload: workloadOf(pod), Pod: pod.Name, Reason: reason}
}

// failingReason is why a pod is failing, or "" if it is not.
func failingReason(pod *corev1.Pod) string {
	for _, s := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		if reason := containerFailing(s); reason != "" {
			return reason
		}
	}
	return ""
}

// containerFailing is why a container is failing, or "". Between restarts a
// crash-looping container spends much of its time terminated rather than
// waiting in CrashLoopBackOff; counted healthy then, its outage would start
// over at every check.
func containerFailing(s corev1.ContainerStatus) string {
	if w := s.State.Waiting; w != nil && slices.Contains(failingReasons, w.Reason) {
		return w.Reason
	}
	if t := s.State.Terminated; t != nil && t.ExitCode != 0 && s.RestartCount > 0 {
		if t.Reason == "OOMKilled" {
			return "OOMKilled"
		}
		return "CrashLoopBackOff"
	}
	return ""
}

// workloadOf names the workload a pod belongs to, from its controller:
// namespace/kind/name, with a Deployment's pods named after the Deployment
// rather than its ReplicaSet. A pod no controller owns is namespace/pod/name.
func workloadOf(pod *corev1.Pod) string {
	o := metav1.GetControllerOfNoCopy(pod)
	if o == nil {
		return pod.Namespace + "/pod/" + pod.Name
	}
	kind, name := strings.ToLower(o.Kind), o.Name
	if o.Kind == "ReplicaSet" {
		if trimmed, ok := strings.CutSuffix(name, "-"+pod.Labels["pod-template-hash"]); ok && pod.Labels["pod-template-hash"] != "" {
			kind, name = "deployment", trimmed
		}
	}
	return pod.Namespace + "/" + kind + "/" + name
}

func describe(ctx context.Context, cs kubernetes.Interface, p assistant.FailingPod) (*assistant.WorkloadState, []string, error) {
	ns, _, _ := strings.Cut(p.Workload, "/")
	pod, err := cs.CoreV1().Pods(ns).Get(ctx, p.Pod, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	state := &assistant.WorkloadState{Name: p.Workload, Pod: pod.Name}

	var failing *corev1.ContainerStatus
	for _, s := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		c := assistant.ContainerState{Name: s.Name, Image: s.Image, State: containerState(s.State), Restarts: s.RestartCount}
		if t := s.LastTerminationState.Terminated; t != nil {
			c.LastTermination = fmt.Sprintf("exit %d: %s", t.ExitCode, t.Reason)
		}
		state.Containers = append(state.Containers, c)
		if failing == nil && containerFailing(s) != "" {
			failing = &s
		}
	}
	if failing != nil {
		state.Logs = containerLogs(ctx, cs, pod, *failing)
	}
	return state, podEvents(ctx, cs, pod), nil
}

func containerState(s corev1.ContainerState) string {
	switch {
	case s.Waiting != nil:
		return strings.TrimSuffix("waiting: "+s.Waiting.Reason+" "+s.Waiting.Message, " ")
	case s.Terminated != nil:
		return fmt.Sprintf("terminated: exit %d, %s", s.Terminated.ExitCode, s.Terminated.Reason)
	case s.Running != nil:
		return "running"
	}
	return "unknown"
}

// containerLogs reads the container's last lines, those of the run that
// crashed. A log that cannot be read is left out.
func containerLogs(ctx context.Context, cs kubernetes.Interface, pod *corev1.Pod, s corev1.ContainerStatus) []string {
	tail, limit := int64(logLines), int64(maxLogBytes)
	// While it waits to restart, the run that crashed is the previous one;
	// once terminated, it is the current one.
	previous := s.State.Waiting != nil && s.RestartCount > 0
	opts := &corev1.PodLogOptions{Container: s.Name, TailLines: &tail, LimitBytes: &limit, Previous: previous}
	stream, err := cs.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, opts).Stream(ctx)
	if err != nil {
		return nil
	}
	defer func() { _ = stream.Close() }()
	raw, err := io.ReadAll(io.LimitReader(stream, maxLogBytes))
	if err != nil {
		return nil
	}
	text := strings.TrimRight(string(raw), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// podEvents lists the pod's warning events, oldest first.
func podEvents(ctx context.Context, cs kubernetes.Interface, pod *corev1.Pod) []string {
	list, err := cs.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + pod.Name + ",type=" + corev1.EventTypeWarning})
	if err != nil {
		return nil
	}
	events := slices.DeleteFunc(list.Items, func(e corev1.Event) bool {
		return e.Type != corev1.EventTypeWarning || e.InvolvedObject.Name != pod.Name
	})
	sort.Slice(events, func(i, j int) bool { return eventTime(events[i]).Before(eventTime(events[j])) })
	events = events[max(0, len(events)-maxEvents):]
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = fmt.Sprintf("%s %s: %s", eventTime(e).UTC().Format(time.RFC3339), e.Reason, e.Message)
	}
	return out
}
