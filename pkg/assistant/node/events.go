package node

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// maxEvents bounds the Kubernetes events in a snapshot.
const maxEvents = 20

// warningEvents lists the newest Kubernetes warning events, oldest first.
func warningEvents(ctx context.Context, cs kubernetes.Interface) ([]string, error) {
	list, err := cs.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "type=" + corev1.EventTypeWarning})
	if err != nil {
		return nil, err
	}
	events := slices.DeleteFunc(list.Items, func(e corev1.Event) bool { return e.Type != corev1.EventTypeWarning })
	sort.Slice(events, func(i, j int) bool { return eventTime(events[i]).Before(eventTime(events[j])) })
	events = events[max(0, len(events)-maxEvents):]

	out := make([]string, len(events))
	for i, e := range events {
		out[i] = fmt.Sprintf("%s %s/%s/%s %s: %s", eventTime(e).UTC().Format(time.RFC3339),
			e.Namespace, strings.ToLower(e.InvolvedObject.Kind), e.InvolvedObject.Name, e.Reason, e.Message)
	}
	return out, nil
}

// WarningEvents returns a Collector.Events that reads from the API server.
func WarningEvents(cs kubernetes.Interface) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) { return warningEvents(ctx, cs) }
}

func eventTime(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	return e.EventTime.Time
}
