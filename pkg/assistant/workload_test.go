package assistant

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeWorkloads is a cluster whose failing pods a test controls.
type fakeWorkloads struct {
	failing   []FailingPod
	described []string
}

func (f *fakeWorkloads) watcher(t *testing.T) (*WorkloadWatcher, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	return &WorkloadWatcher{
		Failing: func(context.Context) ([]FailingPod, error) { return f.failing, nil },
		Describe: func(_ context.Context, p FailingPod) (*WorkloadState, []string, error) {
			f.described = append(f.described, p.Workload)
			return &WorkloadState{Name: p.Workload, Pod: p.Pod, Logs: []string{"panic: no config"}}, []string{"BackOff: Back-off restarting failed container"}, nil
		},
		DataDir:    t.TempDir(),
		Store:      NewStore(t.TempDir()),
		Threshold:  5 * time.Minute,
		MaxPerHour: 2,
		now:        func() time.Time { return now },
	}, &now
}

func crashing(workload string) FailingPod {
	return FailingPod{Workload: workload, Pod: strings.ReplaceAll(workload, "/", "-") + "-x1", Reason: "CrashLoopBackOff"}
}

func TestAFailingWorkloadIsAnIncidentOnceItLasts(t *testing.T) {
	f := &fakeWorkloads{failing: []FailingPod{crashing("shop/deployment/api")}}
	w, now := f.watcher(t)
	ctx := context.Background()

	w.check(ctx)
	*now = now.Add(4 * time.Minute)
	w.check(ctx)
	if n := len(listed(t, w.Store)); n != 0 {
		t.Fatalf("recorded %d before the threshold", n)
	}
	*now = now.Add(2 * time.Minute)
	w.check(ctx)
	*now = now.Add(time.Minute)
	w.check(ctx) // still failing: the same outage

	all := listed(t, w.Store)
	if len(all) != 1 {
		t.Fatalf("recorded %d incidents, want 1", len(all))
	}
	inc := all[0]
	if inc.Kind != KindWorkload || inc.Trigger != "shop/deployment/api failing for 6m0s: CrashLoopBackOff" {
		t.Errorf("incident = %s %q", inc.Kind, inc.Trigger)
	}
	if inc.Snapshot.Workload == nil || inc.Snapshot.Workload.Logs[0] != "panic: no config" || len(inc.Snapshot.Events) != 1 {
		t.Errorf("snapshot = %+v", inc.Snapshot)
	}
}

func TestAWorkloadThatRecoversIsAnIncidentAgainNextTime(t *testing.T) {
	f := &fakeWorkloads{}
	w, now := f.watcher(t)
	ctx := context.Background()

	for range 2 {
		f.failing = []FailingPod{crashing("shop/deployment/api")}
		w.check(ctx)
		*now = now.Add(6 * time.Minute)
		w.check(ctx)
		f.failing = nil
		*now = now.Add(time.Minute)
		w.check(ctx)
		*now = now.Add(time.Hour) // past the hourly limit
	}
	if n := len(listed(t, w.Store)); n != 2 {
		t.Errorf("recorded %d incidents, want 2", n)
	}
}

// A cluster-wide failure must not flood the store and the model: past the
// hourly limit, failing workloads wait for room, and are recorded then if
// they still fail.
func TestWorkloadIncidentsAreRateLimited(t *testing.T) {
	f := &fakeWorkloads{}
	for i := range 5 {
		f.failing = append(f.failing, crashing(fmt.Sprintf("shop/deployment/app%d", i)))
	}
	w, now := f.watcher(t)
	ctx := context.Background()

	w.check(ctx)
	*now = now.Add(6 * time.Minute)
	w.check(ctx)
	if n := len(listed(t, w.Store)); n != 2 {
		t.Fatalf("recorded %d incidents in the first hour, want the limit, 2", n)
	}
	*now = now.Add(time.Hour)
	w.check(ctx)
	if n := len(listed(t, w.Store)); n != 4 {
		t.Errorf("recorded %d after an hour, want 4", n)
	}
}

func TestPodsOfOneWorkloadAreOneIncident(t *testing.T) {
	f := &fakeWorkloads{failing: []FailingPod{crashing("shop/deployment/api"), crashing("shop/deployment/api")}}
	f.failing[1].Pod = "api-x2"
	w, now := f.watcher(t)
	ctx := context.Background()

	w.check(ctx)
	*now = now.Add(6 * time.Minute)
	w.check(ctx)
	if n := len(listed(t, w.Store)); n != 1 {
		t.Errorf("recorded %d incidents for one workload's two pods, want 1", n)
	}
}
