package node

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEventsAreRecentWarningsOnly(t *testing.T) {
	base := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	var objs []corev1.Event
	for i := range maxEvents + 5 {
		objs = append(objs, corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: fmt.Sprintf("e%d", i), Namespace: "kube-system"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: fmt.Sprintf("coredns-%d", i)},
			Type:           corev1.EventTypeWarning,
			Reason:         "BackOff",
			Message:        "Back-off restarting failed container",
			LastTimestamp:  metav1.NewTime(base.Add(time.Duration(i) * time.Second)),
		})
	}
	objs = append(objs, corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "normal", Namespace: "default"},
		Type:       corev1.EventTypeNormal, Reason: "Pulled", LastTimestamp: metav1.NewTime(base.Add(time.Hour)),
	})
	cs := fake.NewSimpleClientset(&corev1.EventList{Items: objs})

	got, err := warningEvents(context.Background(), cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxEvents {
		t.Fatalf("got %d events, want %d", len(got), maxEvents)
	}
	if !strings.Contains(got[len(got)-1], fmt.Sprintf("coredns-%d", maxEvents+4)) || !strings.Contains(got[0], "BackOff") {
		t.Errorf("want the newest warnings, oldest first: %q ... %q", got[0], got[len(got)-1])
	}
	for _, e := range got {
		if strings.Contains(e, "Pulled") {
			t.Errorf("normal event included: %q", e)
		}
	}
}
